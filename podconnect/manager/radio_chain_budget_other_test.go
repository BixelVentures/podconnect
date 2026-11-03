//go:build !linux

package main

func radioChainBudgetPath(sender *radioSender) string { panic("explicit Linux external fixture only") }
