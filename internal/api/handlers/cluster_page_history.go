package handlers

// The Histories of the cluster page's tabs and of the objects in their rows.
//
// A tab's History is every row of the resource types the tab holds, in the
// path's cluster (SelectTypes), told apart by type: its categories are the
// tab's kinds of object. A row's History is one object's own rows
// (SelectObjects), told apart by what happened to it: its categories are made
// of the pieces below. Both read only audit rows today; the changes made in
// Proxmox that nothing on record explains come in as observed rows, under the
// kind's ObservedTypes.
//
// The audit identity of each object — the resource type and the id its rows
// are filed under — is documented on AuditLog (common.go) and spelled by
// internal/auditkey; a selector here names the same pair.

// ruleCreatedDeleted puts an object's creation and removal in "lifecycle".
// The plain verbs are the generic handlers' (created, deleted); the typed ones
// carry the object in front of them (sdn_zone_created, rule_deleted,
// osd_create, mon_destroy).
var ruleCreatedDeleted = HistoryRule{Category: "lifecycle", Field: HistoryFieldAction,
	Equals: []string{"created", "deleted", "already_deleted", "create", "delete"},
	Like:   []string{`%\_created`, `%\_deleted`, `%\_create`, `%\_destroy`}}

// ruleUpdated puts a change to an object's settings in "config".
var ruleUpdated = HistoryRule{Category: "config", Field: HistoryFieldAction,
	Equals: []string{"updated", "update"}, Like: []string{`%\_updated`, `%\_update`}}

// ruleObserved puts an edit made in Proxmox, which nothing on record explains,
// in "config" too: a kind that shows observed changes (ObservedTypes) says so.
var ruleObserved = HistoryRule{Category: "config", Field: HistoryFieldEntry, Equals: []string{"observed"}}

// typeRule puts the rows of the resource types in category: a tab's History
// tells its kinds of object apart by it.
func typeRule(category string, types ...string) HistoryRule {
	return HistoryRule{Category: category, Field: HistoryFieldResourceType, Equals: types}
}

// WithObjectWindow gives a kind of the cluster page the window and the caps of
// a folder's and a node's History: it opens on the last week (all_time asks for
// the rest), and reads no more than objectHistoryRowCap rows of each source
// whatever the window. The routes are assembled with it.
func WithObjectWindow(k HistoryKind) HistoryKind {
	k.DefaultWindow, k.RowCap, k.FilteredRowCap = objectHistoryDefaultWindow, objectHistoryRowCap, objectHistoryRowCap
	return k
}

// observedOf declares types as the observed types of a kind, each read under
// its own view permission (observedTypePermission).
func observedOf(types ...string) []HistoryObservedType {
	observed := make([]HistoryObservedType, len(types))
	for i, t := range types {
		observed[i] = HistoryObservedType{Type: t}
	}
	return observed
}

// objectCategories are the categories of the History of one object that has
// nothing but its own creation, removal and settings: what a row's drawer
// offers for most objects.
var objectCategories = []string{"lifecycle", "config", "other"}

// objectRules are objectCategories' rules.
var objectRules = []HistoryRule{ruleCreatedDeleted, ruleUpdated}

// objectHistory is the kind of an object whose History is its own audit rows,
// told apart by what happened to them: the path parameter param names it, by
// the id its rows are filed under, in each of types. observed lists the types
// whose changes seen in Proxmox the kind shows too.
func objectHistory(name, param string, observed []string, types ...string) HistoryKind {
	objects := make([]HistoryObject, len(types))
	for i, t := range types {
		objects[i] = HistoryObject{ResourceType: t}
	}
	rules := objectRules
	if len(observed) > 0 {
		rules = append(append([]HistoryRule{}, objectRules...), ruleObserved)
	}
	return HistoryKind{
		Name:          name,
		Categories:    objectCategories,
		Default:       "other",
		Rules:         rules,
		Select:        SelectObjects(param, objects...),
		ObjectTypes:   types,
		ObservedTypes: observedOf(observed...),
	}
}
