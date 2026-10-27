package handlers

import (
	"context"
	"net/url"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

// CephHistory is the Ceph tab's kind: the cluster's OSDs, pools, monitors,
// managers, metadata servers and filesystems, told apart by type.
func CephHistory() HistoryKind {
	types := []string{"ceph_osd", "ceph_pool", "ceph_mon", "ceph_mgr", "ceph_mds", "ceph_fs"}
	return HistoryKind{
		Name:       "ceph",
		Categories: []string{"osds", "pools", "monitors", "filesystems", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("osds", "ceph_osd"),
			typeRule("pools", "ceph_pool"),
			typeRule("monitors", "ceph_mon", "ceph_mgr"),
			typeRule("filesystems", "ceph_mds", "ceph_fs"),
		},
		Select:        SelectTypes(types...),
		ObjectTypes:   types,
		ObservedTypes: observedOf("ceph_osd", "ceph_pool"),
	}
}

// CephOSDHistory is one OSD's kind: the in, out, start, stop and restart it was
// ordered, its creation and removal, and its state seen to change in Proxmox.
// An OSD is filed under its number; one a node created is filed under the
// device it was made on (the OSD has no number until the task ends), so that
// row is the node's.
func CephOSDHistory() HistoryKind {
	return HistoryKind{
		Name:       "ceph-osd",
		Categories: []string{"lifecycle", "state", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			ruleCreatedDeleted,
			{Category: "state", Field: HistoryFieldAction, Like: []string{`osd\_%`}},
			{Category: "state", Field: HistoryFieldEntry, Equals: []string{"observed"}},
		},
		Select: func(_ context.Context, r HistoryRequest) (HistorySelection, error) {
			id := strconv.FormatInt(r.Params.Int("osd_id"), 10)
			return HistorySelection{Objects: []HistoryObject{{ResourceType: "ceph_osd", Value: id}}}, nil
		},
		ObjectTypes:   []string{"ceph_osd"},
		ObservedTypes: observedOf("ceph_osd"),
	}
}

// CephPoolHistory is one Ceph pool's kind. The pool's name arrives percent-
// encoded where it is not path-safe, and its rows are filed under the name.
func CephPoolHistory() HistoryKind {
	return HistoryKind{
		Name:       "ceph-pool",
		Categories: objectCategories,
		Default:    "other",
		Rules:      append(append([]HistoryRule{}, objectRules...), ruleObserved),
		Select: func(_ context.Context, r HistoryRequest) (HistorySelection, error) {
			name, err := url.PathUnescape(r.Params.String("pool_name"))
			if err != nil {
				return HistorySelection{}, fiber.NewError(fiber.StatusBadRequest, "Malformed pool_name in path")
			}
			return HistorySelection{Objects: []HistoryObject{{ResourceType: "ceph_pool", Value: name}}}, nil
		},
		ObjectTypes:   []string{"ceph_pool"},
		ObservedTypes: observedOf("ceph_pool"),
	}
}

// CephMonitorHistory is one Ceph monitor's kind: its creation and removal.
func CephMonitorHistory() HistoryKind {
	return HistoryKind{
		Name:        "ceph-monitor",
		Categories:  []string{"lifecycle", "other"},
		Default:     "other",
		Rules:       []HistoryRule{ruleCreatedDeleted},
		Select:      SelectObjects("monitor", HistoryObject{ResourceType: "ceph_mon"}),
		ObjectTypes: []string{"ceph_mon"},
	}
}
