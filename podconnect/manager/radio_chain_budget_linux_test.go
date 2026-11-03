//go:build linux

package main

import "path/filepath"

func radioChainBudgetPath(sender *radioSender) string {
	return filepath.Join(sender.containment.(*radioCgroup).path, "memory.max")
}
