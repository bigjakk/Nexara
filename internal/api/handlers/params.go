package handlers

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// Helpers shared by the handlers that take their request through the
// declarative registry (internal/api/registry.go) rather than binding it
// to an ad-hoc struct.
//
// Every one of them takes a value the handler has ALREADY read with a
// Params accessor, rather than taking the *apischema.Params and the key.
// That is not a style preference: registry_paramkey_guard_test.go walks
// each handler's source for accessor calls whose key is a string literal
// and checks it against the endpoint's declared schema, and a key passed
// through a helper is a key the guard cannot see. Keeping p.String("x")
// at the point of use keeps every parameter name inside the guard's
// reach.

// parseParamUUID converts a parameter that the schema declared with
// apischema's "uuid" format into a uuid.UUID.
//
// The format has already rejected anything uuid.Parse could choke on, so
// a failure here does not mean the caller sent something bad — it means
// the endpoint's declaration and this call disagree about the parameter,
// which is our bug and no request can fix. It is reported the way
// Endpoint.validationError reports the same class of mistake: a 500,
// with nothing about our schema handed to whoever happened to be calling.
func parseParamUUID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, fiber.NewError(fiber.StatusInternalServerError, "Request validation failed")
	}
	return id, nil
}

// optBoolPtr turns an OptBool read into the *bool a Proxmox parameter
// struct uses to mean "send this key only if the caller chose".
//
// It takes both return values of p.OptBool positionally, so the call
// reads optBoolPtr(p.OptBool("numa")).
func optBoolPtr(value, supplied bool) *bool {
	if !supplied {
		return nil
	}
	return &value
}

// optIntPtr is optBoolPtr for an integer parameter, read the same way:
// optIntPtr(p.OptInt("balloon")).
func optIntPtr(value int64, supplied bool) *int {
	if !supplied {
		return nil
	}
	n := int(value)
	return &n
}

// optInt64Ptr is optIntPtr for a parameter whose Proxmox struct field is a
// *int64 rather than a *int — an expiry, which is a Unix timestamp.
//
// It is a separate function rather than a generic one because the two exist to
// produce two DIFFERENT types, and a caller picking the wrong width silently
// truncates on a 32-bit build.
func optInt64Ptr(value int64, supplied bool) *int64 {
	if !supplied {
		return nil
	}
	return &value
}

// optStringPtr is optBoolPtr for a string parameter, read the same way:
// optStringPtr(p.OptString("comment")).
//
// The EMPTY STRING is a supplied value here, not an absent one, and that
// is the point rather than an accident: a form that sends comment:"" means
// "clear the note", and a helper that folded "" into nil would silently
// turn every such save into a no-op. apischema draws the same line — see
// present() in validate.go.
func optStringPtr(value string, supplied bool) *string {
	if !supplied {
		return nil
	}
	return &value
}

// intListFromStrings converts the string form of a validated
// integer-array parameter into the ints a JSONB column — and the API's own
// number[] response shape — need.
//
// Params.Strings renders every element whatever its declared type, so an
// Array of Integer comes back as decimal text. A failure here is not a bad
// request: the schema has already refused anything that is not an integer
// inside the declared bounds. It means the declaration and this call
// disagree about the parameter, which is our bug and no request can fix,
// so it is reported the way parseParamUUID reports the same class of
// mistake — a 500, with nothing about our schema handed to the caller.
//
// nil in, nil out: a caller who omitted an optional array gets no list,
// which the caller distinguishes from an empty one if it needs to.
func intListFromStrings(values []string) ([]int, error) {
	if values == nil {
		return nil, nil
	}
	out := make([]int, 0, len(values))
	for _, v := range values {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fiber.NewError(fiber.StatusInternalServerError, "Request validation failed")
		}
		out = append(out, n)
	}
	return out, nil
}

// uuidListFromStrings converts a validated uuid-array parameter into the
// uuid.UUIDs the queries take.
//
// It is intListFromStrings for the other element type, and fails the same
// way and for the same reason: the schema's "uuid" format has already
// refused anything uuid.Parse could choke on, so an error here is a
// disagreement between the declaration and this call rather than something
// the caller got wrong.
//
// Unlike intListFromStrings it returns an EMPTY slice for an empty input
// rather than nil, because its one caller stores the result as "the
// channel set is now this" — and a nil there would be indistinguishable
// from "the caller said nothing", which is the distinction the Has check
// at the call site exists to make.
func uuidListFromStrings(values []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(values))
	for _, v := range values {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, fiber.NewError(fiber.StatusInternalServerError, "Request validation failed")
		}
		out = append(out, id)
	}
	return out, nil
}

// stringMap flattens an object parameter into the map[string]string a
// Proxmox configuration write takes.
//
// A scalar element is rendered rather than refused: JSON has no way to
// spell "the string 2" differently from "the number 2" once a client has
// been through a form, and a caller sending cores as a number means the
// same thing as one sending "2". A nested object or array IS refused,
// because there is no honest rendering of one into a config value and
// silently writing "map[]" into a guest's configuration is worse than a
// 400. field names the parameter so the message says which one.
func stringMap(field string, in map[string]any) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for key, raw := range in {
		text, ok := scalarText(raw)
		if !ok {
			return nil, fiber.NewError(fiber.StatusBadRequest,
				fmt.Sprintf("%s.%s: expected a string or a number, not a nested object or array", field, key))
		}
		out[key] = text
	}
	return out, nil
}

// scalarText renders one JSON scalar, reporting false for anything that
// is not one. The number cases keep integers integral: encoding/json is
// read with UseNumber (see registry_params.go), so a whole number arrives
// as json.Number and stays "2" rather than becoming "2e+00".
func scalarText(raw any) (string, bool) {
	switch v := raw.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case nil:
		return "", true
	default:
		return "", false
	}
}

// guestIDs reads the two identifiers every per-guest registry route
// carries in its path.
func guestIDs(p *apischema.Params) (clusterID, guestID uuid.UUID, err error) {
	clusterID, err = parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	guestID, err = parseParamUUID(p.String("vm_id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return clusterID, guestID, nil
}

// containerIDs is guestIDs for the /containers/ routes, which spell the
// guest's path parameter :ct_id.
//
// It is a separate function rather than a parameter on guestIDs because
// registry_paramkey_guard_test.go walks a handler's callees for accessor
// keys and checks them against THAT endpoint's schema: a shared helper
// reading whichever of two literal keys it was told to would either read a
// non-literal key (invisible to the guard) or read "vm_id" on a route that
// declares "ct_id" (a guaranteed false failure). Two functions, two
// literals, both checkable.
func containerIDs(p *apischema.Params) (clusterID, ctID uuid.UUID, err error) {
	clusterID, err = parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	ctID, err = parseParamUUID(p.String("ct_id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return clusterID, ctID, nil
}

// StringMapFromObject narrows an Object parameter to the map[string]string a
// group-to-role mapping actually is.
//
// apischema carries an Object through UNVALIDATED — there is no
// nested-properties field, so the declaration asserts only "this is a JSON
// object" — and the values arrive as whatever JSON produced: a string, a
// json.Number, a bool. The columns these maps are written to are read back with
// json.Unmarshal into a map[string]string, and that read is best-effort: one
// non-string value makes the WHOLE mapping decode to nothing, silently, at
// login time rather than at write time.
//
// The struct field this replaces was a map[string]string, so the JSON decoder
// refused such a body with a 400. This is that refusal, kept — and it names the
// offending key rather than the whole body.
//
// It takes the VALUE plus the field name rather than the *apischema.Params and
// the key, for the reason given at the top of this file: a parameter name that
// only ever appears inside a helper is a name registry_paramkey_guard_test.go
// cannot check against the schema. field is used solely for the message.
func StringMapFromObject(raw map[string]any, field string) (map[string]string, error) {
	if raw == nil {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(raw))
	for name, value := range raw {
		s, ok := value.(string)
		if !ok {
			return nil, fiber.NewError(fiber.StatusBadRequest,
				field+": every value must be a string (\""+name+"\" is not)")
		}
		out[name] = s
	}
	return out, nil
}
