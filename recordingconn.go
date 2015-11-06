package runner

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
)

// recordingConn is a net.Conn that records the traffic that passes through it.
// WriteTo can be used to produce output that can be later be loaded with
// ParseTestData.
type recordingConn struct {
	net.Conn
	sync.Mutex
	flows   [][]byte
	reading bool
}

func (r *recordingConn) Read(b []byte) (n int, err error) {
	if n, err = r.Conn.Read(b); n == 0 {
		return
	}
	b = b[:n]

	r.Lock()
	defer r.Unlock()

	if l := len(r.flows); l == 0 || !r.reading {
		buf := make([]byte, len(b))
		copy(buf, b)
		r.flows = append(r.flows, buf)
	} else {
		r.flows[l-1] = append(r.flows[l-1], b[:n]...)
	}
	r.reading = true
	return
}

func (r *recordingConn) Write(b []byte) (n int, err error) {
	if n, err = r.Conn.Write(b); n == 0 {
		return
	}
	b = b[:n]

	r.Lock()
	defer r.Unlock()

	if l := len(r.flows); l == 0 || r.reading {
		buf := make([]byte, len(b))
		copy(buf, b)
		r.flows = append(r.flows, buf)
	} else {
		r.flows[l-1] = append(r.flows[l-1], b[:n]...)
	}
	r.reading = false
	return
}

// WriteTo writes hex dumps to w that contains the recorded traffic.
func (r *recordingConn) WriteTo(w io.Writer) {
	// TLS always starts with a client to server flow.
	clientToServer := true

	for i, flow := range r.flows {
		source, dest := "client", "server"
		if !clientToServer {
			source, dest = dest, source
		}
		fmt.Fprintf(w, ">>> Flow %d (%s to %s)\n", i+1, source, dest)
		dumper := hex.Dumper(w)
		dumper.Write(flow)
		dumper.Close()
		clientToServer = !clientToServer
	}
}

func parseTestData(r io.Reader) (flows [][]byte, err error) {
	var currentFlow []byte

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		// If the line starts with ">>> " then it marks the beginning
		// of a new flow.
		if strings.HasPrefix(line, ">>> ") {
			if len(currentFlow) > 0 || len(flows) > 0 {
				flows = append(flows, currentFlow)
				currentFlow = nil
			}
			continue
		}

		// Otherwise the line is a line of hex dump that looks like:
		// 00000170  fc f5 06 bf (...)  |.....X{&?......!|
		// (Some bytes have been omitted from the middle section.)

		if i := strings.IndexByte(line, ' '); i >= 0 {
			line = line[i:]
		} else {
			return nil, errors.New("invalid test data")
		}

		if i := strings.IndexByte(line, '|'); i >= 0 {
			line = line[:i]
		} else {
			return nil, errors.New("invalid test data")
		}

		hexBytes := strings.Fields(line)
		for _, hexByte := range hexBytes {
			val, err := strconv.ParseUint(hexByte, 16, 8)
			if err != nil {
				return nil, errors.New("invalid hex byte in test data: " + err.Error())
			}
			currentFlow = append(currentFlow, byte(val))
		}
	}

	if len(currentFlow) > 0 {
		flows = append(flows, currentFlow)
	}

	return flows, nil
}
