package client

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/han/qrush/internal/protocol"
)

type ListResult struct {
	Jobs     []protocol.JobInfo
	MaxSlots int
}

func recvError(msg *protocol.Msg) error {
	p, err := protocol.PayloadAs[protocol.PayloadError](msg)
	if err != nil {
		return fmt.Errorf("server error (malformed response)")
	}
	return fmt.Errorf("%s", p.Message)
}

func ListJobs() (*ListResult, error) {
	c, err := Connect()
	if err != nil {
		return nil, err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{Type: protocol.MsgList}); err != nil {
		return nil, err
	}

	result := &ListResult{}
	for {
		msg, err := c.Recv()
		if err != nil {
			return nil, err
		}
		switch msg.Type {
		case protocol.MsgListLine:
			payload, pErr := protocol.PayloadAs[protocol.PayloadListLine](msg)
			if pErr != nil {
				return nil, pErr
			}
			result.Jobs = append(result.Jobs, payload.Job)
		case protocol.MsgListEnd:
			payload, pErr := protocol.PayloadAs[protocol.PayloadSlots](msg)
			if pErr != nil {
				return nil, pErr
			}
			result.MaxSlots = payload.Slots
			return result, nil
		case protocol.MsgError:
			return nil, recvError(msg)
		default:
			return nil, fmt.Errorf("unexpected message: %v", msg.Type)
		}
	}
}

func KillServer() error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Send(&protocol.Msg{Type: protocol.MsgKillServer})
}

func ClearFinished() error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{Type: protocol.MsgClearFinished}); err != nil {
		return err
	}
	return recvOK(c)
}

func GetVersion() (int, error) {
	c, err := Connect()
	if err != nil {
		return 0, err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{Type: protocol.MsgGetVersion}); err != nil {
		return 0, err
	}
	msg, err := c.Recv()
	if err != nil {
		return 0, err
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadVersion](msg)
	if pErr != nil {
		return 0, pErr
	}
	return payload.Version, nil
}

func RemoveJob(id int) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgRemoveJob,
		Payload: protocol.PayloadJobID{JobID: id},
	}); err != nil {
		return err
	}
	msg, err := c.Recv()
	if err != nil {
		return err
	}
	if msg.Type == protocol.MsgError {
		return recvError(msg)
	}
	return nil
}

// Rerun re-enqueues a copy of an existing job and returns the new job ID.
func Rerun(id int) (int, error) {
	c, err := Connect()
	if err != nil {
		return -1, err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgRerun,
		Payload: protocol.PayloadJobID{JobID: id},
	}); err != nil {
		return -1, err
	}
	msg, err := c.Recv()
	if err != nil {
		return -1, err
	}
	if msg.Type == protocol.MsgError {
		return -1, recvError(msg)
	}
	payload, perr := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if perr != nil {
		return -1, perr
	}
	return payload.JobID, nil
}

func GetState(id int) (protocol.JobState, error) {
	c, err := Connect()
	if err != nil {
		return 0, err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgGetState,
		Payload: protocol.PayloadJobID{JobID: id},
	}); err != nil {
		return 0, err
	}
	msg, err := c.Recv()
	if err != nil {
		return 0, err
	}
	if msg.Type == protocol.MsgError {
		return 0, recvError(msg)
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadState](msg)
	if pErr != nil {
		return 0, pErr
	}
	return payload.State, nil
}

func GetOutput(id int) (string, error) {
	c, err := Connect()
	if err != nil {
		return "", err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgAskOutput,
		Payload: protocol.PayloadJobID{JobID: id},
	}); err != nil {
		return "", err
	}
	msg, err := c.Recv()
	if err != nil {
		return "", err
	}
	if msg.Type == protocol.MsgError {
		return "", recvError(msg)
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadOutput](msg)
	if pErr != nil {
		return "", pErr
	}
	return payload.Filename, nil
}

func CatOutput(id int) error {
	filename, err := GetOutput(id)
	if err != nil {
		return err
	}
	if filename == "" {
		return fmt.Errorf("no output file for job %d", id)
	}

	for {
		if _, err := os.Stat(filename); err == nil {
			break
		}
		state, err := GetState(id)
		if err != nil {
			return err
		}
		if state == protocol.StateFinished || state == protocol.StateSkipped {
			if _, statErr := os.Stat(filename); statErr != nil {
				return fmt.Errorf("job finished but no output file")
			}
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, 4096)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			os.Stdout.Write(buf[:n])
		}
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		if readErr == io.EOF {
			state, stateErr := GetState(id)
			if stateErr != nil {
				return reportExitStatus(id)
			}
			if state == protocol.StateFinished || state == protocol.StateSkipped {
				for {
					n, _ := f.Read(buf)
					if n == 0 {
						break
					}
					os.Stdout.Write(buf[:n])
				}
				return reportExitStatus(id)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func reportExitStatus(id int) error {
	info, err := GetInfo(id)
	if err != nil {
		return nil
	}
	if info.State == protocol.StateSkipped {
		fmt.Fprintf(os.Stderr, "ru: job %d was skipped (dependency failed)\n", id)
		return nil
	}
	if info.State != protocol.StateFinished {
		return nil
	}
	if info.Result.DiedBySignal {
		fmt.Fprintf(os.Stderr, "ru: job %d killed by signal %d\n", id, info.Result.Signal)
		return nil
	}
	if info.Result.ExitCode != 0 {
		fmt.Fprintf(os.Stderr, "ru: job %d exited with code %d\n", id, info.Result.ExitCode)
	}
	return nil
}

func ShowOutputFile(id int) error {
	filename, err := GetOutput(id)
	if err != nil {
		return err
	}
	fmt.Println(filename)
	return nil
}

func GetInfo(id int) (*protocol.JobInfo, error) {
	c, err := Connect()
	if err != nil {
		return nil, err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgInfo,
		Payload: protocol.PayloadJobID{JobID: id},
	}); err != nil {
		return nil, err
	}
	msg, err := c.Recv()
	if err != nil {
		return nil, err
	}
	if msg.Type == protocol.MsgError {
		return nil, recvError(msg)
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadInfo](msg)
	if pErr != nil {
		return nil, pErr
	}
	return &payload.Job, nil
}

func GetPID(id int) (int, error) {
	info, err := GetInfo(id)
	if err != nil {
		return 0, err
	}
	return info.PID, nil
}

func SetMaxSlots(n int) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgSetMaxSlots,
		Payload: protocol.PayloadSlots{Slots: n},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

func GetMaxSlots() (int, error) {
	c, err := Connect()
	if err != nil {
		return 0, err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{Type: protocol.MsgGetMaxSlots}); err != nil {
		return 0, err
	}
	msg, err := c.Recv()
	if err != nil {
		return 0, err
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadSlots](msg)
	if pErr != nil {
		return 0, pErr
	}
	return payload.Slots, nil
}

func MakeUrgent(id int) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgUrgent,
		Payload: protocol.PayloadJobID{JobID: id},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

func SwapJobs(id1, id2 int) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgSwapJobs,
		Payload: protocol.PayloadSwap{ID1: id1, ID2: id2},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

func WaitJob(id int) (*protocol.Result, error) {
	c, err := Connect()
	if err != nil {
		return nil, err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgWaitJob,
		Payload: protocol.PayloadJobID{JobID: id},
	}); err != nil {
		return nil, err
	}
	msg, err := c.Recv()
	if err != nil {
		return nil, err
	}
	if msg.Type == protocol.MsgError {
		return nil, recvError(msg)
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadResult](msg)
	if pErr != nil {
		return nil, pErr
	}
	return &payload.Result, nil
}

func KillJob(id int) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgKillJob,
		Payload: protocol.PayloadJobID{JobID: id},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

func KillAllJobs() error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{Type: protocol.MsgKillAll}); err != nil {
		return err
	}
	return recvOK(c)
}

func CountRunning() (int, error) {
	c, err := Connect()
	if err != nil {
		return 0, err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{Type: protocol.MsgCountRunning}); err != nil {
		return 0, err
	}
	msg, err := c.Recv()
	if err != nil {
		return 0, err
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadCount](msg)
	if pErr != nil {
		return 0, pErr
	}
	return payload.Count, nil
}

func GetLabel(id int) (string, error) {
	c, err := Connect()
	if err != nil {
		return "", err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgGetLabel,
		Payload: protocol.PayloadJobID{JobID: id},
	}); err != nil {
		return "", err
	}
	msg, err := c.Recv()
	if err != nil {
		return "", err
	}
	if msg.Type == protocol.MsgError {
		return "", recvError(msg)
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadLabel](msg)
	if pErr != nil {
		return "", pErr
	}
	return payload.Label, nil
}

// SetJobLabel renames an existing job (sets its label).
func SetJobLabel(id int, label string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgSetJobLabel,
		Payload: protocol.PayloadSetLabel{JobID: id, Label: label},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

// SetJobTimeout changes a job's wall-clock timeout (0 clears it); it takes
// effect when the job (re)starts.
func SetJobTimeout(id int, timeoutMS int64) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgSetJobTimeout,
		Payload: protocol.PayloadSetTimeout{JobID: id, TimeoutMS: timeoutMS},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

func LastID() (int, error) {
	c, err := Connect()
	if err != nil {
		return 0, err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{Type: protocol.MsgLastID}); err != nil {
		return 0, err
	}
	msg, err := c.Recv()
	if err != nil {
		return 0, err
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadJobID](msg)
	if pErr != nil {
		return 0, pErr
	}
	return payload.JobID, nil
}

func GetCmd(id int) (string, error) {
	c, err := Connect()
	if err != nil {
		return "", err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgGetCmd,
		Payload: protocol.PayloadJobID{JobID: id},
	}); err != nil {
		return "", err
	}
	msg, err := c.Recv()
	if err != nil {
		return "", err
	}
	if msg.Type == protocol.MsgError {
		return "", recvError(msg)
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadCmd](msg)
	if pErr != nil {
		return "", pErr
	}
	return payload.Cmd, nil
}

func GetEnv(key string) (string, error) {
	c, err := Connect()
	if err != nil {
		return "", err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgGetEnv,
		Payload: protocol.PayloadEnv{Key: key},
	}); err != nil {
		return "", err
	}
	msg, err := c.Recv()
	if err != nil {
		return "", err
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadEnv](msg)
	if pErr != nil {
		return "", pErr
	}
	return payload.Value, nil
}

func SetEnv(key, value string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgSetEnv,
		Payload: protocol.PayloadEnv{Key: key, Value: value},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

func UnsetEnv(key string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgUnsetEnv,
		Payload: protocol.PayloadEnv{Key: key},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

func GetLogdir() (string, error) {
	c, err := Connect()
	if err != nil {
		return "", err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{Type: protocol.MsgGetLogdir}); err != nil {
		return "", err
	}
	msg, err := c.Recv()
	if err != nil {
		return "", err
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadLogdir](msg)
	if pErr != nil {
		return "", pErr
	}
	return payload.Path, nil
}

func SetLogdir(path string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgSetLogdir,
		Payload: protocol.PayloadLogdir{Path: path},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

// ResetServer factory-resets the daemon: kills all jobs and panes, drops all
// sessions/groups, and restores default settings.
func ResetServer() error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{Type: protocol.MsgReset}); err != nil {
		return err
	}
	return recvOK(c)
}

func recvOK(c *Client) error {
	msg, err := c.Recv()
	if err != nil {
		return err
	}
	if msg.Type == protocol.MsgError {
		return recvError(msg)
	}
	return nil
}
