package client

import "github.com/han/qrush/internal/protocol"

func AttachTerminal(session, pane string, cols, rows int) (*Client, error) {
	c, err := Connect()
	if err != nil {
		return nil, err
	}
	if err := c.Send(&protocol.Msg{
		Type: protocol.MsgTerminalAttach,
		Payload: protocol.PayloadTerminalAttach{
			Session: session,
			Pane:    pane,
			Cols:    cols,
			Rows:    rows,
		},
	}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// OpenTerminal asks the daemon to create a fresh pane in a session and returns
// its server-assigned, restart-stable name.
func OpenTerminal(session string, cols, rows int) (string, error) {
	c, err := Connect()
	if err != nil {
		return "", err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgTerminalOpen,
		Payload: protocol.PayloadTerminalOpen{Session: session, Cols: cols, Rows: rows},
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
	payload, pErr := protocol.PayloadAs[protocol.PayloadTerminalName](msg)
	if pErr != nil {
		return "", pErr
	}
	return payload.Pane, nil
}

// GetTerminalLayout returns a session's persisted layout blob and the names of
// its still-alive panes.
func GetTerminalLayout(session string) ([]byte, []string, error) {
	c, err := Connect()
	if err != nil {
		return nil, nil, err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgTerminalGetLayout,
		Payload: protocol.PayloadTerminalGetLayout{Session: session},
	}); err != nil {
		return nil, nil, err
	}
	msg, err := c.Recv()
	if err != nil {
		return nil, nil, err
	}
	if msg.Type == protocol.MsgError {
		return nil, nil, recvError(msg)
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadTerminalLayout](msg)
	if pErr != nil {
		return nil, nil, pErr
	}
	return payload.Blob, payload.Alive, nil
}

// SetTerminalLayout persists a session's layout blob; keep lists the pane names
// that should survive (the daemon reaps the rest).
func SetTerminalLayout(session string, blob []byte, keep []string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgTerminalSetLayout,
		Payload: protocol.PayloadTerminalSetLayout{Session: session, Blob: blob, Keep: keep},
	}); err != nil {
		return err
	}
	return recvOK(c)
}

// ListTerminals returns every live daemon-hosted pane across all sessions.
func ListTerminals() ([]protocol.TerminalInfo, error) {
	c, err := Connect()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{Type: protocol.MsgTerminalListAll}); err != nil {
		return nil, err
	}
	msg, err := c.Recv()
	if err != nil {
		return nil, err
	}
	if msg.Type == protocol.MsgError {
		return nil, recvError(msg)
	}
	payload, pErr := protocol.PayloadAs[protocol.PayloadTerminalListAll](msg)
	if pErr != nil {
		return nil, pErr
	}
	return payload.Terminals, nil
}

func KillTerminal(session, pane string) error {
	c, err := Connect()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Send(&protocol.Msg{
		Type:    protocol.MsgTerminalKill,
		Payload: protocol.PayloadTerminalKill{Session: session, Pane: pane},
	}); err != nil {
		return err
	}
	return recvOK(c)
}
