package native

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestRPCBrokerAndEventHubOverPipe(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readyCh := make(chan *Handler, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunHostWithOptions(ctx, inR, outW, HostOptions{
			OnReady: func(_ context.Context, h *Handler) (func(), error) {
				readyCh <- h
				return nil, nil
			},
		})
	}()

	extWrite := NewConn(nil, inW)
	extRead := NewConn(outR, nil)

	h := <-readyCh
	subID, evCh := h.Events().Subscribe()
	defer h.Events().Unsubscribe(subID)

	// 1. Test extension -> host tb_event propagation to EventHub.
	if err := extWrite.Write(Request{
		Method: "tb_event",
		Params: json.RawMessage(`{"event":"mail.new","data":{"accountId":"acc1"}}`),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-evCh:
		if ev.Event != "mail.new" {
			t.Fatalf("expected mail.new event, got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for EventHub event")
	}

	// 2. Test host -> extension tb_rpc reverse-RPC round-trip.
	rpcDone := make(chan json.RawMessage, 1)
	rpcErr := make(chan error, 1)
	go func() {
		callCtx, callCancel := context.WithTimeout(ctx, 2*time.Second)
		defer callCancel()
		res, err := h.RPC().Call(callCtx, "accounts.list", map[string]any{"x": 1})
		if err != nil {
			rpcErr <- err
			return
		}
		rpcDone <- res
	}()

	var wireResp Response
	if err := extRead.Read(&wireResp); err != nil {
		t.Fatal(err)
	}
	if wireResp.Event != "tb_rpc" || wireResp.ID == "" {
		t.Fatalf("expected tb_rpc frame, got %+v", wireResp)
	}

	resParams, _ := json.Marshal(map[string]any{
		"id":     wireResp.ID,
		"ok":     true,
		"result": map[string]any{"accounts": []string{"acc1"}},
	})
	if err := extWrite.Write(Request{
		Method: "tb_rpc_result",
		Params: resParams,
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-rpcErr:
		t.Fatalf("RPC failed: %v", err)
	case raw := <-rpcDone:
		if string(raw) != `{"accounts":["acc1"]}` {
			t.Fatalf("unexpected RPC result: %s", string(raw))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RPC result")
	}

	_ = inW.Close()
	_ = outR.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("RunHostWithOptions returned error: %v", err)
	}
}
