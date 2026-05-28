package session

// RFC 4254 wire-format structs for ssh.Marshal / ssh.Unmarshal.

type envRequest struct {
	Name  string
	Value string
}

type execRequest struct {
	Command string
}

type subsystemRequest struct {
	Name string
}

type signalRequest struct {
	Signal string
}

type ptyRequest struct {
	Term     string
	Columns  uint32
	Rows     uint32
	Width    uint32
	Height   uint32
	Modelist string
}

type windowChangeRequest struct {
	Columns uint32
	Rows    uint32
	Width   uint32
	Height  uint32
}

type exitStatusMessage struct {
	Status uint32
}

// RFC 4254 §6.10. "Lang" must be present (possibly empty) on the wire.
type exitSignalMessage struct {
	Signal     string
	CoreDumped bool
	ErrorMsg   string
	Lang       string
}
