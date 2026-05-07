package handlers

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"strings"

	// Blank-imported so image.DecodeConfig recognises png and jpeg encodings
	// when validating raster uploads (defends against polyglot files that
	// pass http.DetectContentType but contain malformed bitstreams).
	_ "image/jpeg"
	_ "image/png"
)

// extToMIME maps an accepted upload extension to the exact
// http.DetectContentType output that must match the file body. SVG is
// intentionally absent — it must be validated via parseAndValidateSVG instead,
// since http.DetectContentType has no SVG signature.
//
// ICO is matched strictly (not by "image/" prefix) so that an SVG body with a
// .ico extension does not slip through with a relaxed MIME prefix check.
var extToMIME = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".ico":  "image/x-icon",
}

// rasterExtsRequiringDecode are the formats Go's image package can decode
// natively (with the blank imports above). Decoding the config rejects
// polyglots — a PNG header followed by trailing arbitrary bytes still sniffs
// as image/png via http.DetectContentType but fails image.DecodeConfig if the
// bitstream is malformed. WebP is intentionally absent: pulling
// golang.org/x/image/webp in for one decode path is heavy, and WebP's RIFF
// container is itself a fairly strict signature for http.DetectContentType.
var rasterExtsRequiringDecode = map[string]struct{}{
	".png":  {},
	".jpg":  {},
	".jpeg": {},
}

// validateImageUpload checks that the uploaded byte slice matches the claimed
// extension. For SVG it runs the strict XML validator; for raster formats it
// uses http.DetectContentType for fast rejection AND image.DecodeConfig for
// structural validation. PNGs masquerading as .svg (and vice versa) are
// rejected, as are polyglot files (PNG header + trailing JS/HTML).
func validateImageUpload(content []byte, ext string) error {
	ext = strings.ToLower(ext)
	if ext == ".svg" {
		return parseAndValidateSVG(content)
	}
	expected, ok := extToMIME[ext]
	if !ok {
		return fmt.Errorf("unsupported image extension: %s", ext)
	}
	detected := http.DetectContentType(content)
	// strip the optional ";charset=…" suffix the sniffer never emits for image/* but
	// future-proofs against a Go stdlib change.
	if i := strings.Index(detected, ";"); i >= 0 {
		detected = strings.TrimSpace(detected[:i])
	}
	if detected != expected {
		return fmt.Errorf("file content (%s) does not match extension %s", detected, ext)
	}
	if _, ok := rasterExtsRequiringDecode[ext]; ok {
		if _, _, err := image.DecodeConfig(bytes.NewReader(content)); err != nil {
			return fmt.Errorf("file is not a valid %s image: %w", strings.TrimPrefix(ext, "."), err)
		}
	}
	return nil
}

// svgDeniedElements are SVG / SVG-adjacent element local-names that can run
// script, embed external content, or pivot navigation.
var svgDeniedElements = map[string]struct{}{
	"script":           {},
	"foreignobject":    {},
	"iframe":           {},
	"embed":            {},
	"object":           {},
	"audio":            {},
	"video":            {},
	"handler":          {}, // SVG 1.2 Tiny
	"animate":          {}, // SMIL — abusable via from/to/values + xlink:href
	"animatemotion":    {},
	"animatetransform": {},
	"set":              {},
	"style":            {}, // CSS expressions / @import in older browsers
}

// safeDataURIPrefixes are the only data: URI MIME types we permit inside
// href/xlink:href attributes. Everything else is rejected.
var safeDataURIPrefixes = []string{
	"data:image/png;",
	"data:image/jpeg;",
	"data:image/gif;",
	"data:image/webp;",
}

// parseAndValidateSVG token-walks the input as XML and rejects any construct
// that could lead to script execution or external resource loading when the
// SVG is rendered in a browsing context (i.e. navigated to directly, not just
// embedded via <img>).
func parseAndValidateSVG(content []byte) error {
	if len(content) == 0 {
		return errors.New("svg is empty")
	}

	dec := xml.NewDecoder(bytes.NewReader(content))
	// Strict mode catches malformed XML early; we deliberately leave Strict at
	// its default (true) and never set Entity, so external entity declarations
	// fail closed.
	dec.Strict = true
	// Disallow auto-closing rules from HTML — we want true XML.
	dec.AutoClose = nil

	sawSVGRoot := false

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("svg is not well-formed XML: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if !sawSVGRoot {
				if name != "svg" {
					return fmt.Errorf("svg root element must be <svg>, got <%s>", t.Name.Local)
				}
				if t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg" {
					return fmt.Errorf("svg root element has wrong namespace %q", t.Name.Space)
				}
				sawSVGRoot = true
			}
			if _, denied := svgDeniedElements[name]; denied {
				return fmt.Errorf("svg contains disallowed element <%s>", t.Name.Local)
			}
			for _, attr := range t.Attr {
				if err := validateSVGAttribute(t.Name.Local, attr); err != nil {
					return err
				}
			}
		case xml.Directive:
			// <!DOCTYPE ...> and <!ENTITY ...> live here. Reject doctypes to
			// kill any external/internal DTD smuggling vector outright.
			lower := strings.ToLower(strings.TrimSpace(string(t)))
			if strings.HasPrefix(lower, "doctype") || strings.HasPrefix(lower, "entity") {
				return errors.New("svg contains disallowed DOCTYPE/ENTITY declaration")
			}
		case xml.ProcInst:
			// <?xml-stylesheet ...?> can pull in remote CSS.
			if strings.EqualFold(t.Target, "xml-stylesheet") {
				return errors.New("svg contains disallowed xml-stylesheet processing instruction")
			}
		}
	}

	if !sawSVGRoot {
		return errors.New("svg has no <svg> root element")
	}
	return nil
}

func validateSVGAttribute(elem string, attr xml.Attr) error {
	name := strings.ToLower(attr.Name.Local)
	value := strings.TrimSpace(attr.Value)

	// Any event handler (onload, onclick, onmouseover, …).
	if strings.HasPrefix(name, "on") {
		return fmt.Errorf("svg <%s> uses disallowed event handler attribute %q", elem, attr.Name.Local)
	}

	// href / xlink:href can navigate to javascript: or fetch external resources.
	if name == "href" || (strings.EqualFold(attr.Name.Space, "http://www.w3.org/1999/xlink") && name == "href") {
		return validateSVGURIRef(elem, attr.Name.Local, value)
	}

	// Any namespaced *:href we missed (older Inkscape/Adobe outputs).
	if strings.HasSuffix(name, "href") && attr.Name.Space != "" {
		return validateSVGURIRef(elem, attr.Name.Local, value)
	}

	// style="" can carry CSS expressions in legacy browsers, and url(...) refs
	// can pull external resources. Block anything containing url() that isn't
	// a fragment, plus the obvious script vectors.
	if name == "style" {
		return validateSVGInlineStyle(elem, value)
	}
	return nil
}

func validateSVGURIRef(elem, attrName, raw string) error {
	if raw == "" {
		return nil
	}
	// Browsers ignore C0 controls (\x00-\x1F), \x7F, and ASCII whitespace
	// when parsing URI schemes — `\tjavascript:alert(1)` or `javascript:`
	// reach the same handler as `javascript:`. TrimLeftFunc strips them so the
	// scheme inspection sees what the browser sees.
	v := strings.TrimLeftFunc(raw, func(r rune) bool {
		return r <= 0x20 || r == 0x7F
	})
	v = strings.ToLower(v)
	if v == "" {
		return nil
	}

	// Fragment-only references stay in-document and can't pivot.
	if strings.HasPrefix(v, "#") {
		return nil
	}
	// data:image/{png,jpeg,gif,webp} only.
	if strings.HasPrefix(v, "data:") {
		for _, prefix := range safeDataURIPrefixes {
			if strings.HasPrefix(v, prefix) {
				return nil
			}
		}
		return fmt.Errorf("svg <%s> %s uses disallowed data: URI", elem, attrName)
	}
	// Branding SVGs only need fragment refs and inline image data: URIs.
	// Anything else — schemed (javascript:, http:, file:, mailto:, …) or
	// relative path-style (../, /api/…, foo.png) — is rejected.
	return fmt.Errorf("svg <%s> %s references external or schemed resource %q", elem, attrName, raw)
}

func validateSVGInlineStyle(elem, value string) error {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "javascript:") || strings.Contains(lower, "vbscript:") || strings.Contains(lower, "expression(") {
		return fmt.Errorf("svg <%s> style attribute contains disallowed scripting", elem)
	}
	// url(...) is allowed only when the target is a fragment ref (#id) or an
	// allowed data: URI; everything else is blocked to prevent external fetches.
	for {
		idx := strings.Index(lower, "url(")
		if idx < 0 {
			break
		}
		rest := lower[idx+len("url("):]
		end := strings.Index(rest, ")")
		if end < 0 {
			return fmt.Errorf("svg <%s> style has unterminated url(...)", elem)
		}
		arg := strings.TrimSpace(rest[:end])
		arg = strings.Trim(arg, `"'`)
		switch {
		case strings.HasPrefix(arg, "#"):
			// fragment OK
		case strings.HasPrefix(arg, "data:"):
			ok := false
			for _, prefix := range safeDataURIPrefixes {
				if strings.HasPrefix(arg, prefix) {
					ok = true
					break
				}
			}
			if !ok {
				return fmt.Errorf("svg <%s> style url(...) uses disallowed data: URI", elem)
			}
		default:
			return fmt.Errorf("svg <%s> style references external resource %q", elem, arg)
		}
		lower = rest[end+1:]
	}
	return nil
}
