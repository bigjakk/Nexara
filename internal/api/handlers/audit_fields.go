package handlers

import (
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strings"
)

// auditFieldNames is the names, never the values, of the Proxmox fields an
// update sends, for its audit row's fields: the change observer explains only
// those, and the ones Proxmox derives from them, as changed by the row
// (internal/collector observer_explain.go). view:audit is held by every
// Viewer, so a name is all a row may carry.
//
// params is a request struct of the proxmox package, its fields tagged with
// Proxmox's names: each that is set counts — a non-nil pointer, a non-empty
// string or list, a non-zero number — embedded structs included. Or it is the
// form itself, every key of it. Either way the names a delete list takes out
// count too, as does each of also: a name the request clears, or sends
// whatever its value. digest, the config's checksum, is no field.
func auditFieldNames(params any, also ...string) []string {
	names := map[string]bool{}
	add := func(name string) {
		if name != "" && name != "digest" {
			names[name] = true
		}
	}
	for _, name := range also {
		add(name)
	}
	if form, ok := params.(url.Values); ok {
		for key, values := range form {
			if key != "delete" {
				add(key)
				continue
			}
			for _, v := range values {
				for _, name := range auditSplitList(v) {
					add(name)
				}
			}
		}
	} else {
		auditStructFieldNames(reflect.ValueOf(params), add)
	}
	return slices.Sorted(maps.Keys(names))
}

func auditStructFieldNames(v reflect.Value, add func(string)) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	for i := range v.NumField() {
		field, value := v.Type().Field(i), v.Field(i)
		if !field.IsExported() || value.IsZero() {
			continue
		}
		if field.Anonymous {
			auditStructFieldNames(value, add)
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		switch {
		case name == "" || name == "-":
		case name != "delete":
			add(name)
		case value.Kind() == reflect.String:
			for _, n := range auditSplitList(value.String()) {
				add(n)
			}
		case value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.String:
			for _, n := range value.Interface().([]string) {
				add(n)
			}
		}
	}
}

// auditSplitList splits a list as Proxmox does (PVE::Tools::split_list): on
// commas, semicolons and white space.
func auditSplitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' })
}
