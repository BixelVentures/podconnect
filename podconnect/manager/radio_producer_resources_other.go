//go:build !linux

package main

func (s *radioSender) productionEvidence(int) error { return errRadioUnavailable }
