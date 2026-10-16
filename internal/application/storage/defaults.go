package storage

import _ "embed"

// The frozen initial configuration is bootstrap data. TASK-002-05 owns its
// revisioned validator and plan/apply authority; no policy is executed here.
//
//go:embed defaults.json
var defaults []byte

func DefaultConfiguration() []byte { return append([]byte(nil), defaults...) }
