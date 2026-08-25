package handlers

import "github.com/gofiber/fiber/v3"

// ListResponse is the envelope every collection endpoint returns.
//
// One shape for every list, rather than the mix of bare arrays,
// {items,total} and {entries,total} this API grew: an external consumer had
// to branch on the response type per route, and the same logical resource
// disagreed with itself across two routes (/clusters/:id/audit-log returned a
// bare array while /audit-log returned {items,total}). Bare arrays also had
// nowhere to put a count, so a caller could not tell a full page from a
// truncated one.
//
// Total is the number of rows matching the request's filters before
// limit/offset — not len(Items). For endpoints that proxy Proxmox and have no
// separate count to issue, the two coincide; RespondItems is the shorthand for
// exactly that case.
//
// TestGuard_ListEndpointsUseEnvelope keeps new handlers from reintroducing a
// bare array.
type ListResponse[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
}

// RespondList writes a collection with a total that may exceed len(items),
// i.e. any endpoint that paginates.
//
// A nil slice is normalised to an empty one so `items` is always a JSON array
// and never null — a caller iterating the field should not have to nil-check
// it.
func RespondList[T any](c fiber.Ctx, items []T, total int64) error {
	if items == nil {
		items = []T{}
	}
	return c.JSON(ListResponse[T]{Items: items, Total: total})
}

// RespondItems writes a collection returned in full, setting Total to its
// length. Use it for endpoints with no pagination; use RespondList where a
// separate count query bounds the result.
func RespondItems[T any](c fiber.Ctx, items []T) error {
	return RespondList(c, items, int64(len(items)))
}
