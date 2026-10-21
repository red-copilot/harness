package pi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestResetSendsNewSessionCommand(t *testing.T) {
	h := newRPCHarness(t)
	session := h.session(t)
	var sawNewSession bool
	h.onCommand = func(command map[string]any) {
		if command["type"] == "new_session" {
			sawNewSession = true
			h.write(t, `{"id":"`+command["id"].(string)+`","type":"response","command":"new_session","success":true,"data":{}}`)
			return
		}
		h.write(t, `{"id":"`+command["id"].(string)+`","type":"response","command":"`+command["type"].(string)+`","success":true,"data":{"disposition":"started"}}`)
	}
	resettable, ok := session.(interface{ Reset(context.Context) error })
	if !ok {
		t.Fatal("Pi session does not implement Reset")
	}
	if err := resettable.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if !sawNewSession {
		t.Fatal("new_session command was not sent")
	}
}

func TestResetFailsWhenPiRejectsNewSession(t *testing.T) {
	h := newRPCHarness(t)
	session := h.session(t)
	h.onCommand = func(command map[string]any) {
		if command["type"] == "new_session" {
			h.write(t, `{"id":"`+command["id"].(string)+`","type":"response","command":"new_session","success":false,"error":"reset denied"}`)
			return
		}
		h.write(t, `{"id":"`+command["id"].(string)+`","type":"response","command":"`+command["type"].(string)+`","success":true,"data":{}}`)
	}
	resettable := session.(interface{ Reset(context.Context) error })
	err := resettable.Reset(context.Background())
	if err == nil || !strings.Contains(err.Error(), "reset denied") {
		t.Fatalf("err = %v, want reset denied", err)
	}
}

func TestResetUsesStrictJSONCommand(t *testing.T) {
	h := newRPCHarness(t)
	session := h.session(t)
	got := make(chan map[string]any, 1)
	h.onCommand = func(command map[string]any) {
		got <- command
		data, _ := json.Marshal(command)
		_ = data
		h.write(t, `{"id":"`+command["id"].(string)+`","type":"response","command":"new_session","success":true,"data":{}}`)
	}
	if err := session.(interface{ Reset(context.Context) error }).Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	command := <-got
	if command["type"] != "new_session" {
		t.Fatalf("command = %+v", command)
	}
}
