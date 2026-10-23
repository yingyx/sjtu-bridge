package servicecatalog

// mergeReviewedRecords updates an existing semantic task without breaking the
// explicit indices used by older portal/navigation projections.
func mergeReviewedRecords(base, updates []record) []record {
	for _, updated := range updates {
		index := -1
		for i := range base {
			if base[i].entry.ID == updated.entry.ID {
				index = i
				break
			}
		}
		if index < 0 {
			base = append(base, updated)
			continue
		}
		old := base[index]
		destinations := updated.entry.Destinations
		updated.entry.Destinations = old.entry.Destinations
		remap := map[int]int{}
		for i, d := range destinations {
			remap[i] = addDestination(&updated.entry, d)
		}
		updated.entry.SupportedCapabilityIDs = appendUnique(updated.entry.SupportedCapabilityIDs, old.entry.SupportedCapabilityIDs...)
		updated.entry.Aliases = appendUnique(updated.entry.Aliases, old.entry.Aliases...)
		updated.entry.Keywords = appendUnique(updated.entry.Keywords, old.entry.Keywords...)
		for _, b := range updated.portals {
			b.destination = remap[b.destination]
			found := false
			for _, prior := range old.portals {
				if prior.sourceID == b.sourceID {
					found = true
				}
			}
			if !found {
				old.portals = append(old.portals, b)
			}
		}
		for _, b := range updated.navigation {
			b.destination = remap[b.destination]
			old.navigation = append(old.navigation, b)
		}
		updated.portals, updated.navigation = old.portals, old.navigation
		base[index] = updated
	}
	return base
}
