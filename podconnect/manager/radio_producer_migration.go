package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
)

// Opposite-protocol rows can remain immutable history only after the old
// protocol's exact terminal proof. This never adopts a receiver or a source.
func producerLegacyHistoryTerminal(records map[string]radioProducerRecord, record radioProducerRecord) bool {
	if record.Primary != nil || !record.Terminal {
		return false
	}
	switch record.Outcome {
	case "committed":
		// A sender STOP by itself can leave the Native Spotify writer parked.
		return producerHistoryReleased(records, record)
	case "preempted":
		return record.Preempted && validProducerNativeRelease(record)
	case "rejected":
		if record.Native.Handoff != "" || record.Preempted {
			return false
		}
		switch record.Stage {
		case "preflight-rejected", "preflight-reconciled":
			return true
		case "no-effects-rejected":
			var query map[string]any
			if !producerNativeRejectionMatches(record.NativeRejection, nativeProducerWire(record.Request)) ||
				json.Unmarshal(record.Request.Peer.Proof, &query) != nil || query == nil {
				return false
			}
			var cancelled radioProducerSenderReply
			decoder := json.NewDecoder(bytes.NewReader(record.CancellationReceipt))
			decoder.DisallowUnknownFields()
			return decoder.Decode(&cancelled) == nil && decoder.Decode(new(any)) == io.EOF &&
				cancelled.Schema == "podconnect.radio-sender.v1" && cancelled.OK && !cancelled.Physical &&
				cancelled.Cancelled && cancelled.Phase == "cancelled" && producerUUID(cancelled.Process) &&
				cancelled.Process == record.Request.Peer.Process && cancelled.Digest == record.Request.Peer.ReceiverDigest &&
				producerSenderOperationMatches(cancelled, query)
		}
	}
	return false
}

func producerPrimaryHistoryTerminal(records map[string]radioProducerRecord, record radioProducerRecord) bool {
	primary := record.Primary
	if primary == nil || !record.Terminal || record.Preempted {
		return false
	}
	if record.Outcome == "rejected" && record.Stage == "no-effects-rejected" {
		command, receipt := primary.LastCommand, primary.LastReceipt
		if command == nil || receipt == nil || (command.Phase != "prepare_replace" && command.Phase != "stop") || primary.Session != nil ||
			receipt.Phase != "replacement_rejected" || receipt.TeardownCSeq != 0 || receipt.Observation != nil {
			return false
		}
		q := *command
		q.ForPhase = receipt.ForPhase
		return primary.IncumbentSession != nil && q.Session != nil && *q.Session == *primary.IncumbentSession &&
			(q.ForPhase == "" || q.ForPhase == command.Phase) && q.matches(*receipt) &&
			producerPrimaryStopOperation(*receipt, record) && receipt.Process == record.Request.Process
	}
	if record.Outcome != "committed" || record.Stage != "terminal" {
		return false
	}
	if primary.Session == nil {
		return producerPrimaryStopTerminal(record)
	}
	if !record.SourceDrained {
		return false
	}
	if producerUUID(primary.ReleaseActionID) && primary.ReleaseActionID != record.Fence.ActionID && primary.TeardownCSeq > 0 {
		// The passive same-owner release path has no separate Manager
		// command. Its exact old session was checked before this fsync.
		return primary.Session.valid()
	}
	if primary.StopReceipt != nil {
		receipt := *primary.StopReceipt
		stop := records[receipt.Handoff]
		return stop.Primary != nil && stop.Primary.Incumbent == record.Fence.Token &&
			producerPrimaryStopOperation(receipt, stop) && receipt.From == record.Request.Target &&
			producerPrimaryStopReceipt(receipt, record, *primary.Session)
	}
	// Early primary journals retained the positive STOP on the operation that
	// stopped the receiver, rather than also copying it to the playing row.
	for _, stop := range records {
		if stop.Primary != nil && stop.Primary.Incumbent == record.Fence.Token && producerPrimaryStopTerminal(stop) &&
			producerPrimaryStopReceipt(*stop.Primary.LastReceipt, record, *primary.Session) {
			return true
		}
	}
	return false
}

func producerPrimaryStopTerminal(record radioProducerRecord) bool {
	primary := record.Primary
	if primary == nil || !record.Terminal || record.Outcome != "committed" || record.Stage != "terminal" ||
		primary.Session != nil || primary.Station != "" || primary.Incumbent == "" || primary.LastCommand == nil ||
		primary.LastReceipt == nil || primary.LastCommand.Phase != "stop" || primary.LastCommand.Session == nil {
		return false
	}
	q, receipt := *primary.LastCommand, *primary.LastReceipt
	q.ForPhase = receipt.ForPhase
	return (q.ForPhase == "" || q.ForPhase == "stop") && q.matches(receipt) &&
		producerPrimaryStopOperation(receipt, record) && producerPrimaryStopReceipt(receipt, record, *q.Session)
}

func producerPrimaryStopOperation(receipt radioPrimaryReceipt, operation radioProducerRecord) bool {
	return receipt.Handoff == operation.Fence.Token && receipt.Action == operation.Fence.ActionID &&
		receipt.Generation == strconv.FormatUint(operation.Fence.Generation, 10) &&
		receipt.OwnerGeneration == strconv.FormatUint(operation.Fence.OwnerGeneration, 10) &&
		receipt.Target == operation.Request.Target && receipt.From == operation.Request.From &&
		receipt.ExpectedRevision == operation.Request.ExpectedIntent.Revision
}

func producerPrimaryStopReceipt(receipt radioPrimaryReceipt, record radioProducerRecord, session radioPrimarySession) bool {
	owner, err := strconv.ParseUint(receipt.OwnerGeneration, 10, 64)
	return err == nil && owner > 0 && receipt.Schema == primaryRadioSchema && receipt.Process == record.Request.Process && producerUUID(receipt.Action) &&
		receipt.Phase == "released" && receipt.TeardownCSeq > 0 && receipt.Session != nil && receipt.Session.valid() &&
		*receipt.Session == session && receipt.Observation != nil && receipt.Observation.OwnerGeneration == owner && receipt.Observation.State == "stopped" &&
		receipt.Observation.SourceKind == "none"
}
