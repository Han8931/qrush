package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
)

type MsgType int

const (
	MsgKillServer MsgType = iota
	MsgNewJob
	MsgNewJobOK
	MsgNewJobNOK
	MsgRunJob
	MsgRunJobOK
	MsgEndJob
	MsgList
	MsgListGPU
	MsgListLine
	MsgListEnd
	MsgClearFinished
	MsgAskOutput
	MsgAnswerOutput
	MsgRemoveJob
	MsgRemoveJobOK
	MsgWaitJob
	MsgWaitRunningJob
	MsgWaitJobOK
	MsgUrgent
	MsgUrgentOK
	MsgGetState
	MsgAnswerState
	MsgSwapJobs
	MsgSwapJobsOK
	MsgInfo
	MsgInfoData
	MsgSetMaxSlots
	MsgGetMaxSlots
	MsgGetMaxSlotsOK
	MsgGetVersion
	MsgVersion
	MsgCountRunning
	MsgCountRunningOK
	MsgGetLabel
	MsgAnswerLabel
	MsgLastID
	MsgLastIDOK
	MsgKillAll
	MsgKillJob
	MsgGetCmd
	MsgAnswerCmd
	MsgGetEnv
	MsgSetEnv
	MsgUnsetEnv
	MsgAnswerEnv
	MsgSetFreePerc
	MsgGetFreePerc
	MsgGetLogdir
	MsgSetLogdir
	MsgAnswerLogdir
	MsgSerialize
	MsgError
	MsgSessionList
	MsgSessionListOK
	MsgSessionCreate
	MsgSessionCreateOK
	MsgSessionRename
	MsgSessionRenameOK
	MsgSessionDelete
	MsgSessionDeleteOK
	MsgListSession
	MsgClearFinishedSession
	MsgTreeList
	MsgTreeListOK
	MsgActionOK
	MsgGroupList
	MsgGroupListOK
	MsgGroupCreate
	MsgGroupCreateOK
	MsgGroupRename
	MsgGroupRenameOK
	MsgGroupDelete
	MsgGroupDeleteOK
	MsgSessionMove
	MsgSessionMoveOK
	MsgTerminalAttach
	MsgTerminalOutput
	MsgTerminalInput
	MsgTerminalResize
	MsgTerminalExit
	MsgTerminalKill
	MsgTerminalOpen
	MsgTerminalOpenOK
	MsgTerminalGetLayout
	MsgTerminalLayout
	MsgTerminalSetLayout
	MsgTerminalListAll
	MsgTerminalListAllOK
	MsgRerun
	// MsgRequestJobsView asks the daemon to flag that a running interactive TUI
	// should switch to its job-management view on its next poll. Sent by
	// `ru --jobs` when it is launched from inside a qrush-hosted pane.
	MsgRequestJobsView
	MsgRequestJobsViewOK
	// MsgSetJobLabel renames an existing job (sets its label) from the TUI's
	// job-name edit box. Answered with MsgActionOK.
	MsgSetJobLabel
	// MsgTUIAttach registers the sender as the daemon's single active
	// interactive TUI; the connection stays open for the TUI's lifetime.
	// Attaching displaces any previously registered TUI, which is sent
	// MsgTUITakenOver and disconnected (tmux `attach -d` semantics).
	MsgTUIAttach
	MsgTUITakenOver
	// MsgReset factory-resets the daemon: kills all running jobs and panes,
	// drops every job/session/group (defaults kept), and restores default
	// runtime settings. Answered with MsgActionOK.
	MsgReset
	// MsgSetJobTimeout changes a job's wall-clock timeout (0 clears it). It
	// takes effect when the job (re)starts. Answered with MsgActionOK.
	MsgSetJobTimeout
)

type Msg struct {
	Type    MsgType
	Payload interface{}
}

type PayloadNewJob struct {
	Request NewJobRequest
}

type PayloadJobID struct {
	JobID int
}

type PayloadResult struct {
	Result Result
}

type PayloadOutput struct {
	Filename string
	PID      int
}

type PayloadState struct {
	State JobState
}

type PayloadSwap struct {
	ID1 int
	ID2 int
}

type PayloadVersion struct {
	Version int
}

type PayloadSlots struct {
	Slots int
}

type PayloadCount struct {
	Count int
}

type PayloadLabel struct {
	Label string
}

type PayloadSetLabel struct {
	JobID int
	Label string
}

type PayloadSetTimeout struct {
	JobID     int
	TimeoutMS int64
}

type PayloadEnv struct {
	Key   string
	Value string
}

type PayloadLogdir struct {
	Path string
}

type PayloadListLine struct {
	Job JobInfo
}

type PayloadSerialize struct {
	Format ListFormat
	Width  int
}

type PayloadError struct {
	Message string
}

type PayloadCmd struct {
	Cmd string
}

type PayloadWaitJob struct {
	JobID int
}

type PayloadInfo struct {
	Job JobInfo
}

type PayloadEndJob struct {
	JobID  int
	Result Result
}

type PayloadSession struct {
	Name string
}

type PayloadSessionRename struct {
	OldName string
	NewName string
}

type PayloadSessionMove struct {
	Session string
	Group   string
}

type PayloadSessionList struct {
	Sessions []string
}

type PayloadGroupList struct {
	Groups []string
}

type PayloadTreeData struct {
	Groups   []string
	Sessions []SessionInfo
	Jobs     []JobInfo
	MaxSlots int
	// OpenJobsView is set on a tree poll response when another client has asked
	// (via MsgRequestJobsView) for the interactive TUI to open its jobs view.
	// The daemon clears the pending flag once it is reported, so exactly one
	// poll observes it.
	OpenJobsView bool
}

type PayloadTerminalAttach struct {
	Session string
	Pane    string
	Cols    int
	Rows    int
}

type PayloadTerminalData struct {
	Data []byte
}

type PayloadTerminalResize struct {
	Cols int
	Rows int
}

type PayloadTerminalKill struct {
	Session string
	Pane    string
}

// PayloadTerminalOpen requests a fresh pane in a session; the daemon assigns a
// unique, restart-stable pane name and returns it in PayloadTerminalName.
type PayloadTerminalOpen struct {
	Session string
	Cols    int
	Rows    int
}

type PayloadTerminalName struct {
	Pane string
}

type PayloadTerminalGetLayout struct {
	Session string
}

// PayloadTerminalLayout carries the opaque, client-serialized layout blob for a
// session plus the set of pane names whose PTYs are still alive in the daemon.
type PayloadTerminalLayout struct {
	Blob  []byte
	Alive []string
}

// PayloadTerminalSetLayout persists a session's layout blob. Keep lists the pane
// names that should survive; the daemon kills any other pane in that session
// (reaping panes the client closed or that are no longer in the layout).
type PayloadTerminalSetLayout struct {
	Session string
	Blob    []byte
	Keep    []string
}

// TerminalInfo identifies one live daemon-hosted pane.
type TerminalInfo struct {
	Session string
	Pane    string
}

// PayloadTerminalListAll is the reply to MsgTerminalListAll: every live pane
// across all sessions.
type PayloadTerminalListAll struct {
	Terminals []TerminalInfo
}

func init() {
	gob.Register(PayloadNewJob{})
	gob.Register(PayloadJobID{})
	gob.Register(PayloadResult{})
	gob.Register(PayloadOutput{})
	gob.Register(PayloadState{})
	gob.Register(PayloadSwap{})
	gob.Register(PayloadVersion{})
	gob.Register(PayloadSlots{})
	gob.Register(PayloadCount{})
	gob.Register(PayloadLabel{})
	gob.Register(PayloadSetLabel{})
	gob.Register(PayloadSetTimeout{})
	gob.Register(PayloadEnv{})
	gob.Register(PayloadLogdir{})
	gob.Register(PayloadListLine{})
	gob.Register(PayloadSerialize{})
	gob.Register(PayloadError{})
	gob.Register(PayloadCmd{})
	gob.Register(PayloadWaitJob{})
	gob.Register(PayloadInfo{})
	gob.Register(PayloadEndJob{})
	gob.Register(PayloadSession{})
	gob.Register(PayloadSessionRename{})
	gob.Register(PayloadSessionMove{})
	gob.Register(PayloadSessionList{})
	gob.Register(PayloadGroupList{})
	gob.Register(PayloadTreeData{})
	gob.Register(PayloadTerminalAttach{})
	gob.Register(PayloadTerminalData{})
	gob.Register(PayloadTerminalResize{})
	gob.Register(PayloadTerminalKill{})
	gob.Register(PayloadTerminalOpen{})
	gob.Register(PayloadTerminalName{})
	gob.Register(PayloadTerminalGetLayout{})
	gob.Register(PayloadTerminalLayout{})
	gob.Register(PayloadTerminalSetLayout{})
	gob.Register(PayloadTerminalListAll{})
}

func Send(w io.Writer, msg *Msg) error {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(msg); err != nil {
		return fmt.Errorf("encode message: %w", err)
	}

	length := uint32(buf.Len())
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	return nil
}

const MaxMessageSize = 64 * 1024 * 1024 // 64 MB

func Recv(r io.Reader) (*Msg, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}
	if length > MaxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes (max %d)", length, MaxMessageSize)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}

	var msg Msg
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&msg); err != nil {
		return nil, fmt.Errorf("decode message: %w", err)
	}
	return &msg, nil
}

func PayloadAs[T any](msg *Msg) (T, error) {
	payload, ok := msg.Payload.(T)
	if !ok {
		var zero T
		// A server-side error in place of the expected payload should surface
		// as its message, not as a payload-type mismatch.
		if pe, isErr := msg.Payload.(PayloadError); isErr {
			return zero, fmt.Errorf("%s", pe.Message)
		}
		return zero, fmt.Errorf("expected %T, got %T", zero, msg.Payload)
	}
	return payload, nil
}
