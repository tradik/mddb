package main

// metadataEqual checks if two metadata maps are equal
func metadataEqual(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}

	for key, aVals := range a {
		bVals, exists := b[key]
		if !exists {
			return false
		}

		if len(aVals) != len(bVals) {
			return false
		}

		// Check if all values match (order matters)
		for i, aVal := range aVals {
			if aVal != bVals[i] {
				return false
			}
		}
	}

	return true
}

// metadataChanged returns true if metadata has changed and needs reindexing
func metadataChanged(existing, new map[string][]string) bool {
	// If existing is empty, it's a new document - needs indexing
	if len(existing) == 0 && len(new) > 0 {
		return true
	}

	// If both empty, no change
	if len(existing) == 0 && len(new) == 0 {
		return false
	}

	// Check if equal
	return !metadataEqual(existing, new)
}
