package modelcache

import "github.com/inercia/mitto/internal/config"

// CatalogState describes whether a model catalog is known and selectable.
type CatalogState string

const (
	// CatalogAbsent means no catalog is known (nil raw input).
	CatalogAbsent CatalogState = "absent"
	// CatalogEmpty means a trustworthy catalog was advertised with no models.
	CatalogEmpty CatalogState = "empty"
	// CatalogAvailable means at least one model is selectable.
	CatalogAvailable CatalogState = "available"
	// CatalogPolicyEmpty means models exist but the hidden-model policy hides all.
	CatalogPolicyEmpty CatalogState = "policy_empty"
)

// FilteredCatalog is the result of applying a hidden-model policy to a raw catalog.
type FilteredCatalog struct {
	Selectable  []Model
	State       CatalogState
	HiddenCount int
}

// FilterModelCatalog applies policy to raw without mutating it. A nil raw slice
// is an absent catalog; a non-nil empty slice is a trustworthy empty catalog.
// A nil policy hides nothing. Order and model metadata are preserved.
func FilterModelCatalog(policy *config.HiddenModelPolicy, raw []Model) FilteredCatalog {
	if raw == nil {
		return FilteredCatalog{State: CatalogAbsent}
	}
	selectable := config.FilterHiddenModels(policy, raw, func(m Model) (string, string) { return m.ID, m.Name })
	res := FilteredCatalog{Selectable: selectable, HiddenCount: len(raw) - len(selectable)}
	switch {
	case len(raw) == 0:
		res.State = CatalogEmpty
	case len(selectable) == 0:
		res.State = CatalogPolicyEmpty
	default:
		res.State = CatalogAvailable
	}
	return res
}

// HiddenSavedIDs returns the subset of ids that the raw catalog contains and
// the policy hides, preserving input order. IDs absent from raw are genuinely
// unavailable and are not reported. Duplicates are reported once.
func HiddenSavedIDs(policy *config.HiddenModelPolicy, raw []Model, ids []string) []string {
	byID := make(map[string]Model, len(raw))
	for _, m := range raw {
		byID[m.ID] = m
	}
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		m, ok := byID[id]
		if !ok || seen[id] || !policy.Hides(m.ID, m.Name) {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
