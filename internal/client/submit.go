package client

import (
	"fmt"
	"os"
	"strings"

	"github.com/han/qrush/internal/protocol"
)

func SubmitJob(command []string, opts SubmitOpts) (int, error) {
	c, err := Connect()
	if err != nil {
		return -1, fmt.Errorf("connect: %w", err)
	}
	defer c.Close()

	wd, _ := os.Getwd()
	env := os.Environ()
	session := opts.Session
	if session == "" {
		session = os.Getenv("QRUSH_SESSION")
	}

	req := protocol.NewJobRequest{
		Command:        shellJoin(command),
		CommandArgs:    command,
		WorkDir:        wd,
		Environment:    env,
		StoreOutput:    opts.StoreOutput,
		SeparateStderr: opts.SeparateStderr,
		GzipOutput:     opts.GzipOutput,
		DependOn:       opts.DependOn,
		RequireElevel:  opts.RequireElevel,
		Label:          opts.Label,
		Session:        session,
		Message:        opts.Message,
		NumSlots:       opts.NumSlots,
		Logfile:        opts.Logfile,
	}

	err = c.Send(&protocol.Msg{
		Type:    protocol.MsgNewJob,
		Payload: protocol.PayloadNewJob{Request: req},
	})
	if err != nil {
		return -1, fmt.Errorf("send: %w", err)
	}

	resp, err := c.Recv()
	if err != nil {
		return -1, fmt.Errorf("recv: %w", err)
	}

	if resp.Type == protocol.MsgNewJobOK {
		payload := resp.Payload.(protocol.PayloadJobID)
		return payload.JobID, nil
	}

	return -1, fmt.Errorf("unexpected response: %v", resp.Type)
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\n\"'\\;|&$(){}[]<>?*~`!#") {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", "'\\''") + "'"
		} else {
			quoted[i] = a
		}
	}
	return strings.Join(quoted, " ")
}

type SubmitOpts struct {
	StoreOutput    bool
	SeparateStderr bool
	GzipOutput     bool
	DependOn       []int
	RequireElevel  bool
	Label          string
	Session        string
	Message        string
	NumSlots       int
	Logfile        string
}
