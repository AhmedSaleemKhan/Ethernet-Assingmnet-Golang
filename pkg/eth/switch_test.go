package eth_test

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"ethswitch/pkg/eth"
)

// STUDENTS: do not edit this file

func TestBasicRequirements(t *testing.T) {
	data, err := os.ReadFile("switch.go")
	if err != nil {
		t.Fatal(err)
	}
	sourceCode := string(data)

	// requirement - This means channels or the "sync" package is forbidden.
	if strings.Contains(sourceCode, `"sync"`) {
		t.Error("sync package is not allowed")
	}
}

func assertEqualFrame(t *testing.T, a, b *eth.Frame) {
	t.Helper()
	if a.Source != b.Source {
		t.Errorf("Sources do not match: %s != %s", net.HardwareAddr(a.Source[:]), b.Source)
	}
	if a.Destination != b.Destination {
		t.Errorf("Destinations do not match: %s != %s", a.Destination, b.Destination)
	}
	if !bytes.Equal(a.Data, b.Data) {
		t.Errorf("Data does not match")
	}
}

func TestReadWriteFrame(t *testing.T) {
	tests := []struct {
		name  string
		frame eth.Frame
		data  []byte
	}{
		{
			name: "small",
			frame: eth.Frame{
				Destination: eth.MACAddress{1, 2, 3, 4, 5, 6},
				Source:      eth.MACAddress{11, 12, 13, 14, 15, 16},
				Data:        []byte{41, 42, 43},
			},
			data: []byte{1, 2, 3, 4, 5, 6, 11, 12, 13, 14, 15, 16, 0, 3, 41, 42, 43, 0xfd, 0x92, 0xeb, 0x38},
		},

		{
			name: "large",
			frame: eth.Frame{
				Destination: eth.MACAddress{1, 2, 3, 4, 5, 6},
				Source:      eth.MACAddress{11, 12, 13, 14, 15, 16},
				Data:        make([]byte, 1400),
			},
			data: append(append([]byte{1, 2, 3, 4, 5, 6, 11, 12, 13, 14, 15, 16, 0x5, 0x78}, make([]byte, 1400)...), 0x5c, 0x9e, 0xf5, 0xd5),
		},
	}

	buf := &bytes.Buffer{}
	buf.Grow(2000)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("WriteFrame", func(t *testing.T) {
				buf.Reset()
				n, err := eth.WriteFrame(buf, tt.frame)
				if err != nil {
					t.Error(err)
				}
				if n != len(tt.data) {
					t.Errorf("expected %d B but got %d B", len(tt.data), n)
				}
				if !bytes.Equal(tt.data, buf.Bytes()) {
					t.Error("expected frames to be equal")
				}
			})

			t.Run("ReadFrame", func(t *testing.T) {
				buf.Reset()
				buf.Write(tt.data)
				f, err := eth.ReadFrame(buf)
				if err != nil {
					t.Fatal(err)
				}
				if f == nil {
					t.Fatal("frame should not be nil")
				}
				assertEqualFrame(t, f, &tt.frame)
			})
		})
	}
}

func TestReadWriteFrame_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		frame eth.Frame
		data  []byte
	}{
		{
			name: "small",
			frame: eth.Frame{
				Destination: eth.MACAddress{1, 2, 3, 4, 5, 6},
				Source:      eth.MACAddress{11, 12, 13, 14, 15, 16},
				Data:        []byte{41, 42, 43},
			},
			data: []byte{1, 2, 3, 4, 5, 6, 11, 12, 13, 14, 15, 16, 0, 3, 41, 42, 43, 0xfd, 0x92, 0xeb, 0x38},
		},
	}

	buf := &bytes.Buffer{}
	buf.Grow(2000)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			saved := tt.data[14]
			tt.data[14] = 99
			buf.Write(tt.data)  // bad
			tt.data[14] = saved // restore
			buf.Write(tt.data)  // good
			f, err := eth.ReadFrame(buf)
			if err != nil {
				t.Fatal(err)
			}
			if f != nil {
				t.Fatal("frame should be nil")
			}

			// read again
			f, err = eth.ReadFrame(buf)
			if err != nil {
				t.Fatal(err)
			}
			if f == nil {
				t.Fatal("frame should not be nil")
			}
			assertEqualFrame(t, f, &tt.frame)

			// read EOF
			f, err = eth.ReadFrame(buf)
			if !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			if f != nil {
				t.Error("Expected frame to be nil")
			}
		})
	}
}

func expectSize(t *testing.T, s, n int) {
	t.Helper()
	if s != n {
		t.Errorf("expected MAC table size to be %d but it was %d", n, s)
	}
}

func expectFrame(t *testing.T, out io.Reader, frame *eth.Frame) {
	t.Helper()
	gotFrame, err := eth.ReadFrame(out)
	if err != nil {
		t.Fatal(err)
	}
	if frame != nil && gotFrame == nil {
		t.Fatal("Frame was not expected to be nil")
	}
	if frame == nil && gotFrame != nil {
		t.Fatalf("Frame was expected to be nil but got %s", frame)
	}
	assertEqualFrame(t, frame, gotFrame)
}

func writeFrame(t *testing.T, w io.Writer, frame eth.Frame) {
	t.Helper()
	_, err := eth.WriteFrame(w, frame)
	if err != nil {
		t.Error(err)
	}
}

type pipedPort struct {
	io.Reader
	io.WriteCloser

	Input  io.WriteCloser
	Output io.ReadCloser
}

// pipedPort implements Port
var _ eth.Port = (*pipedPort)(nil)

func newPipedPort() pipedPort {
	r, in := io.Pipe()
	out, w := io.Pipe()

	return pipedPort{
		Reader:      r,
		WriteCloser: w,

		Input:  in,
		Output: out,
	}
}

func TestSwitch_Simple(t *testing.T) {
	ngo := runtime.NumGoroutine()
	t.Logf("Starting with %d goroutines", ngo)

	macA := eth.MACAddress{5, 5, 5, 5, 5, 0}
	macB := eth.MACAddress{5, 5, 5, 5, 5, 1}

	// setup the ports
	n := 3
	ports := make([]pipedPort, n)
	for i := range ports {
		ports[i] = newPipedPort()
	}

	sw := eth.NewEthernetSwitch(2, ports[0], ports[1], ports[2])

	errs := make(chan error, 1)
	go func() {
		errs <- sw.Run() // blocks
	}()

	t.Run("discovery", func(t *testing.T) {
		// discovery of macA, broadcast
		frameAB1 := eth.Frame{macA, macB, []byte{1}}
		writeFrame(t, ports[0].Input, frameAB1)
		expectFrame(t, ports[1].Output, &frameAB1)
		expectFrame(t, ports[2].Output, &frameAB1)
		expectSize(t, sw.RunSize(), 1)

		t.Run("unicast", func(t *testing.T) {
			// discovery of macB, unicast
			frameBA3 := eth.Frame{macB, macA, []byte{3}}
			writeFrame(t, ports[1].Input, frameBA3)
			expectFrame(t, ports[0].Output, &frameBA3)
			expectSize(t, sw.RunSize(), 2)

			t.Run("regular", func(t *testing.T) {
				t.Logf("Inside with %d goroutines", runtime.NumGoroutine())
				// no-discovery, unicast
				frameAB2 := eth.Frame{macA, macB, []byte{2}}
				frameBA4 := eth.Frame{macB, macA, []byte{4}}
				writeFrame(t, ports[0].Input, frameAB2)
				writeFrame(t, ports[1].Input, frameBA4)
				expectSize(t, sw.RunSize(), 2)
				// swap reading order
				expectFrame(t, ports[0].Output, &frameBA4)
				expectFrame(t, ports[1].Output, &frameAB2)
			})

			t.Run("drop packets", func(t *testing.T) {
				frameAB10 := eth.Frame{macA, macB, []byte{10}}
				frameAB11 := eth.Frame{macA, macB, []byte{11}}
				frameAB12 := eth.Frame{macA, macB, []byte{12}}
				frameAB13 := eth.Frame{macA, macB, []byte{13}}
				writeFrame(t, ports[0].Input, frameAB10)
				writeFrame(t, ports[0].Input, frameAB11)
				writeFrame(t, ports[0].Input, frameAB12) // will be sent on output port (pulled from queue)
				writeFrame(t, ports[0].Input, frameAB13) // will be dropped
				// Wait for the packets to propagate to the send queue.
				// TODO find a way to signal on this instead of sleeping.
				time.Sleep(time.Millisecond)
				expectFrame(t, ports[1].Output, &frameAB10)
				expectFrame(t, ports[1].Output, &frameAB11)
				expectFrame(t, ports[1].Output, &frameAB12) // last packet to make it
				// expectFrame(t, ports[1].Output, &frameAB13) // dropped
			})
		})
	})

	t.Run("broadcast frame", func(t *testing.T) {
		frameA99 := eth.Frame{macA, eth.BroadcastAddress, []byte{99}}
		writeFrame(t, ports[0].Input, frameA99)
		expectFrame(t, ports[1].Output, &frameA99)
		expectFrame(t, ports[2].Output, &frameA99)
	})

	t.Run("bad frame", func(t *testing.T) {
		invalidFrame := []byte{1, 2, 3, 4, 5, 6, 11, 12, 13, 14, 15, 16, 0, 3, 41, 42, 43, 0xaa, 0x92, 0xeb, 0x38}
		n, err := ports[2].Input.Write(invalidFrame)
		if err != nil {
			t.Error(err)
		}
		if n != len(invalidFrame) {
			t.Error("Switch read only part of a frame")
		}
		// we do not expect this frame to be dropped since the checksum is invalid
	})

	// close all the inputs to start the shutdown process
	for _, p := range ports {
		if err := p.Input.Close(); err != nil {
			t.Error(err)
		}
	}

	// drain the outputs
	for id, p := range ports {
		for {
			f, err := eth.ReadFrame(p.Output)
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				t.Fatal(err)
			}
			t.Errorf("port %d: unexpected frame %s", id, f)
		}
	}

	// there is only one error on the errs channel
	if err := <-errs; err != nil {
		t.Error(err)
	}

	// wait a little to make sure the goroutines have time to finish after their last statement
	time.Sleep(time.Millisecond)
	ngoDiff := runtime.NumGoroutine() - ngo
	t.Logf("Ending with %d goroutines (difference of %d)", ngo, ngoDiff)
	if ngoDiff > 0 {
		t.Log("***** Possible goroutine leak *****")
	}
}

// TODO add a test case for error handling