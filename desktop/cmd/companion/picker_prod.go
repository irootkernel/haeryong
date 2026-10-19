//go:build !haeryong_test_state

package main

import facade "haeryong.local/haeryong/desktop/readerfacade"

// installFixturePicker is a no-op in production builds: the real Wails dialog
// presenter installed in main is the only selection authority and no fixture
// flag exists in an untagged binary.
func installFixturePicker(*facade.ReaderService) {}

// preflightFixturePicker is a no-op in production builds.
func preflightFixturePicker() {}
