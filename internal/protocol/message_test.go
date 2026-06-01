package protocol

import (
	"bytes"
	"testing"
)

func TestSendRecvRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  Msg
	}{
		{
			name: "kill server",
			msg:  Msg{Type: MsgKillServer},
		},
		{
			name: "new job",
			msg: Msg{
				Type: MsgNewJob,
				Payload: PayloadNewJob{
					Request: NewJobRequest{
						Command:     "echo hello",
						CommandArgs: []string{"echo", "hello"},
						StoreOutput: true,
						NumSlots:    1,
					},
				},
			},
		},
		{
			name: "job id",
			msg:  Msg{Type: MsgNewJobOK, Payload: PayloadJobID{JobID: 42}},
		},
		{
			name: "result",
			msg: Msg{
				Type: MsgWaitJobOK,
				Payload: PayloadResult{
					Result: Result{ExitCode: 0, RealTimeMS: 1500},
				},
			},
		},
		{
			name: "error",
			msg:  Msg{Type: MsgError, Payload: PayloadError{Message: "not found"}},
		},
		{
			name: "label",
			msg:  Msg{Type: MsgAnswerLabel, Payload: PayloadLabel{Label: "build"}},
		},
		{
			name: "env",
			msg:  Msg{Type: MsgSetEnv, Payload: PayloadEnv{Key: "FOO", Value: "bar"}},
		},
		{
			name: "slots",
			msg:  Msg{Type: MsgSetMaxSlots, Payload: PayloadSlots{Slots: 4}},
		},
		{
			name: "swap",
			msg:  Msg{Type: MsgSwapJobs, Payload: PayloadSwap{ID1: 1, ID2: 3}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Send(&buf, &tt.msg); err != nil {
				t.Fatalf("Send: %v", err)
			}
			got, err := Recv(&buf)
			if err != nil {
				t.Fatalf("Recv: %v", err)
			}
			if got.Type != tt.msg.Type {
				t.Errorf("type: got %d, want %d", got.Type, tt.msg.Type)
			}
		})
	}
}

func TestPayloadAs(t *testing.T) {
	msg := &Msg{Type: MsgNewJobOK, Payload: PayloadJobID{JobID: 7}}
	p, err := PayloadAs[PayloadJobID](msg)
	if err != nil {
		t.Fatal(err)
	}
	if p.JobID != 7 {
		t.Errorf("expected JobID=7, got %d", p.JobID)
	}
}

func TestPayloadAsWrongType(t *testing.T) {
	msg := &Msg{Type: MsgNewJobOK, Payload: PayloadJobID{JobID: 7}}
	_, err := PayloadAs[PayloadLabel](msg)
	if err == nil {
		t.Error("expected error for wrong payload type")
	}
}

func TestRecvOversizedMessage(t *testing.T) {
	var buf bytes.Buffer
	// Write a length header that exceeds MaxMessageSize
	header := make([]byte, 4)
	header[0] = 0x10 // 256 MB > 64 MB limit
	header[1] = 0x00
	header[2] = 0x00
	header[3] = 0x00
	buf.Write(header)

	_, err := Recv(&buf)
	if err == nil {
		t.Error("expected error for oversized message")
	}
}

func TestSendRecvMultipleMessages(t *testing.T) {
	var buf bytes.Buffer

	msgs := []Msg{
		{Type: MsgList},
		{Type: MsgNewJobOK, Payload: PayloadJobID{JobID: 1}},
		{Type: MsgKillServer},
	}

	for i := range msgs {
		if err := Send(&buf, &msgs[i]); err != nil {
			t.Fatalf("Send[%d]: %v", i, err)
		}
	}

	for i := range msgs {
		got, err := Recv(&buf)
		if err != nil {
			t.Fatalf("Recv[%d]: %v", i, err)
		}
		if got.Type != msgs[i].Type {
			t.Errorf("msg[%d] type: got %d, want %d", i, got.Type, msgs[i].Type)
		}
	}
}
