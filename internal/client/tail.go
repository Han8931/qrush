package client

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/han/qrush/internal/protocol"
)

func TailOutput(id int) error {
	filename, err := GetOutput(id)
	if err != nil {
		return err
	}
	if filename == "" {
		return fmt.Errorf("no output file for job %d", id)
	}

	for {
		_, err := os.Stat(filename)
		if err == nil {
			break
		}
		state, err := GetState(id)
		if err != nil {
			return err
		}
		if state == protocol.StateFinished || state == protocol.StateSkipped {
			_, statErr := os.Stat(filename)
			if statErr != nil {
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
				return nil
			}
			if state == protocol.StateFinished || state == protocol.StateSkipped {
				n, _ := f.Read(buf)
				if n > 0 {
					os.Stdout.Write(buf[:n])
				}
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}
