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
