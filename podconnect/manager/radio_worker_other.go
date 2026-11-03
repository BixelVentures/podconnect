//go:build !linux

package main

func newRadioWorker() (radioProcessContainment, error) { return nil, errRadioUnavailable }
func radioWorkerGuardianEntry()                        {}
