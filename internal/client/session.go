package client

import (
	"fmt"

	"github.com/han/qrush/internal/protocol"
)

func SessionList() ([]string, error) {
	c, err := Connect()
	if err != nil {
		return nil, err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{Type: protocol.MsgSessionList}); err != nil {
		return nil, err
	}
	msg, err := c.Recv()
	if err != nil {
		return nil, err
	}
	if msg.Type == protocol.MsgError {
		return nil, recvError(msg)
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadSessionList](msg)
	if pErr != nil {
		return nil, pErr
	}
	return payload.Sessions, nil
}

func SessionCreate(name string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgSessionCreate,
		Payload: protocol.PayloadSession{Name: name},
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

func SessionRename(oldName, newName string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgSessionRename,
		Payload: protocol.PayloadSessionRename{OldName: oldName, NewName: newName},
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

func SessionDelete(name string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgSessionDelete,
		Payload: protocol.PayloadSession{Name: name},
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

func SessionMove(session, group string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgSessionMove,
		Payload: protocol.PayloadSessionMove{Session: session, Group: group},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

func GroupList() ([]string, error) {
	c, err := Connect()
	if err != nil {
		return nil, err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{Type: protocol.MsgGroupList}); err != nil {
		return nil, err
	}
	msg, err := c.Recv()
	if err != nil {
		return nil, err
	}
	if msg.Type == protocol.MsgError {
		return nil, recvError(msg)
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadGroupList](msg)
	if pErr != nil {
		return nil, pErr
	}
	return payload.Groups, nil
}

func GroupCreate(name string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgGroupCreate,
		Payload: protocol.PayloadSession{Name: name},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

func GroupRename(oldName, newName string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgGroupRename,
		Payload: protocol.PayloadSessionRename{OldName: oldName, NewName: newName},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

func GroupDelete(name string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgGroupDelete,
		Payload: protocol.PayloadSession{Name: name},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

func ListJobsInSession(session string) (*ListResult, error) {
	c, err := Connect()
	if err != nil {
		return nil, err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgListSession,
		Payload: protocol.PayloadSession{Name: session},
	}); err != nil {
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

func ClearFinishedInSession(session string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgClearFinishedSession,
		Payload: protocol.PayloadSession{Name: session},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

func TreeData() (*protocol.PayloadTreeData, error) {
	c, err := Connect()
	if err != nil {
		return nil, err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{Type: protocol.MsgTreeList}); err != nil {
		return nil, err
	}
	msg, err := c.Recv()
	if err != nil {
		return nil, err
	}
	if msg.Type == protocol.MsgError {
		return nil, recvError(msg)
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadTreeData](msg)
	if pErr != nil {
		return nil, pErr
	}
	return &payload, nil
}

// RequestJobsView asks the daemon to flag that a running interactive TUI should
// open its job-management view on its next tree poll.
func RequestJobsView() error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Send(&protocol.Msg{Type: protocol.MsgRequestJobsView}); err != nil {
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
