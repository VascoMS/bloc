package app

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anthdm/hbbft"
)

func TestPersistentLaneControlBypassesBlockedDataWriter(t *testing.T) {
	stop := make(chan struct{})
	dataStream := newBlockingPersistentStream()
	controlStream := &capturePersistentStream{}
	var releaseOnce sync.Once
	releaseData := func() { releaseOnce.Do(func() { close(dataStream.writeRelease) }) }
	writers := newPeerStreamLaneWriters(1, func(_ context.Context, _ uint64, lane persistentStreamLane) (persistentWriteStream, error) {
		if lane == persistentLaneData {
			return dataStream, nil
		}
		return controlStream, nil
	}, stop)
	t.Cleanup(func() {
		close(stop)
		releaseData()
		<-writers.data.done
		<-writers.control.done
	})

	firstData := sendPersistentAsync(writers.data, context.Background(), []byte("large-data-1"))
	<-dataStream.writeStarted
	secondData := sendPersistentAsync(writers.data, context.Background(), []byte("large-data-2"))
	waitForPersistentQueueLength(t, writers.data, 1)
	control := sendPersistentAsync(writers.control, context.Background(), []byte("ready"))

	select {
	case completion := <-control:
		if completion.err != nil || completion.result.EncodedBytes != len("ready") {
			t.Fatalf("control completion = %+v", completion)
		}
	case <-time.After(time.Second):
		t.Fatal("control send waited for blocked data lane")
	}
	select {
	case <-firstData:
		t.Fatal("blocked data send completed before release")
	default:
	}
	releaseData()
	assertPersistentSendSuccess(t, <-firstData, len("large-data-1"), false)
	assertPersistentSendSuccess(t, <-secondData, len("large-data-2"), true)
}

func TestPersistentLaneResetDoesNotResetOrReplayOtherLane(t *testing.T) {
	stop := make(chan struct{})
	wantErr := errors.New("uncertain control write")
	failedControl := &errorAfterWriteStream{writeErr: wantErr}
	replacementControl := &capturePersistentStream{}
	dataStream := &capturePersistentStream{}
	controlOpens := 0
	writers := newPeerStreamLaneWriters(1, func(_ context.Context, _ uint64, lane persistentStreamLane) (persistentWriteStream, error) {
		if lane == persistentLaneData {
			return dataStream, nil
		}
		controlOpens++
		if controlOpens == 1 {
			return failedControl, nil
		}
		return replacementControl, nil
	}, stop)
	t.Cleanup(func() {
		close(stop)
		<-writers.data.done
		<-writers.control.done
	})

	failedPayload := []byte("ready-one")
	if _, err := writers.control.send(context.Background(), failedPayload); !errors.Is(err, wantErr) {
		t.Fatalf("control failure = %v, want %v", err, wantErr)
	}
	if !failedControl.reset {
		t.Fatal("failed control stream was not reset")
	}
	dataFirst, err := writers.data.send(context.Background(), []byte("proof"))
	if err != nil {
		t.Fatal(err)
	}
	assertPersistentSendSuccess(t, persistentSendCompletion{result: dataFirst}, len("proof"), false)
	dataSecond, err := writers.data.send(context.Background(), []byte("echo"))
	if err != nil {
		t.Fatal(err)
	}
	assertPersistentSendSuccess(t, persistentSendCompletion{result: dataSecond}, len("echo"), true)
	if dataStream.reset {
		t.Fatal("control failure reset the data stream")
	}

	next, err := writers.control.send(context.Background(), []byte("ready-two"))
	if err != nil {
		t.Fatal(err)
	}
	assertPersistentSendSuccess(t, persistentSendCompletion{result: next}, len("ready-two"), false)
	if controlOpens != 2 || bytes.Contains(replacementControl.bytes(), failedPayload) {
		t.Fatalf("control replacement opens=%d replay=%t", controlOpens, bytes.Contains(replacementControl.bytes(), failedPayload))
	}
}

func TestClassifyEnvelopeLane(t *testing.T) {
	tests := []struct {
		name string
		env  WireEnvelope
		want persistentStreamLane
	}{
		{name: "proof", env: laneACS(&hbbft.BroadcastMessage{Payload: &hbbft.ProofRequest{}}), want: persistentLaneData},
		{name: "echo", env: laneACS(&hbbft.BroadcastMessage{Payload: &hbbft.EchoRequest{}}), want: persistentLaneData},
		{name: "ready", env: laneACS(&hbbft.BroadcastMessage{Payload: &hbbft.ReadyRequest{}}), want: persistentLaneControl},
		{name: "bval", env: laneACS(&hbbft.AgreementMessage{Message: &hbbft.BvalRequest{}}), want: persistentLaneControl},
		{name: "aux", env: laneACS(&hbbft.AgreementMessage{Message: &hbbft.AuxRequest{}}), want: persistentLaneControl},
		{name: "share", env: WireEnvelope{Kind: "share", Share: &WireShare{}}, want: persistentLaneData},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := classifyEnvelopeLane(test.env)
			if err != nil || got != test.want {
				t.Fatalf("lane = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestClassifyEnvelopeLaneRejectsInvalidEnvelope(t *testing.T) {
	for _, env := range []WireEnvelope{
		{},
		{Kind: "acs"},
		{Kind: "share"},
		{Kind: "acs", ACS: &hbbft.SlotMessage{}},
	} {
		if lane, err := classifyEnvelopeLane(env); err == nil {
			t.Fatalf("invalid envelope classified as %q: %+v", lane, env)
		}
	}
}

func laneACS(payload any) WireEnvelope {
	return WireEnvelope{Kind: "acs", ACS: slotACSMessage(payload)}
}
