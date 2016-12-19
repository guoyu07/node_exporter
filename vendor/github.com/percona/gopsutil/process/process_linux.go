// +build linux

package process

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/percona/gopsutil/cpu"
	"github.com/percona/gopsutil/internal/common"
	"github.com/percona/gopsutil/net"
)

var ErrorNoChildren = errors.New("process does not have children")

const (
	PrioProcess = 0 // linux/resource.h
)

// MemoryInfoExStat is different between OSes
type MemoryInfoExStat struct {
	RSS    uint64 `json:"rss"`    // bytes
	VMS    uint64 `json:"vms"`    // bytes
	Shared uint64 `json:"shared"` // bytes
	Text   uint64 `json:"text"`   // bytes
	Lib    uint64 `json:"lib"`    // bytes
	Data   uint64 `json:"data"`   // bytes
	Dirty  uint64 `json:"dirty"`  // bytes
}

func (m MemoryInfoExStat) String() string {
	s, _ := json.Marshal(m)
	return string(s)
}

type MemoryMapsStat struct {
	Path         string `json:"path"`
	Rss          uint64 `json:"rss"`
	Size         uint64 `json:"size"`
	Pss          uint64 `json:"pss"`
	SharedClean  uint64 `json:"sharedClean"`
	SharedDirty  uint64 `json:"sharedDirty"`
	PrivateClean uint64 `json:"privateClean"`
	PrivateDirty uint64 `json:"privateDirty"`
	Referenced   uint64 `json:"referenced"`
	Anonymous    uint64 `json:"anonymous"`
	Swap         uint64 `json:"swap"`
}

type ProcStat struct {
	PID                      int64   // 1
	Name                     string  // 2
	State                    string  // 3
	ParentPID                int32   // 4
	ProcessGroupID           int64   // 5
	SessionID                int64   // 6
	TTY                      int64   // 7
	ForegroundProcessGroupID int64   // 8
	Flags                    uint64  // 9
	MinorPageFaults          uint64  // 10
	ChildrenMinorPageFaults  uint64  // 11
	MajorPageFaults          uint64  // 12
	ChildreMajorPageFaults   uint64  // 13
	UserTime                 float64 // 14
	SystemTime               float64 // 15
	ChildrenUserTime         float64 // 16
	ChildrenKernelTime       float64 // 17
	Priority                 int64   // 18
	Nice                     int32   // 19
	Threads                  int64   // 20
	ITRealValue              int64
	StartTime                int64
	VSize                    uint64
	RSS                      int64
	RSSLim                   string //
	StartCode                uint64
	EndCode                  uint64
	StartStack               uint64
	KstkESP                  uint64
	KstkIP                   uint64
	Signal                   uint64
	// Blocked                  uint64 obsolete
	// SIGIgnore                uint64 obsolete
	// SIGCatch                 uint64 obsolete
	WChan               uint64
	NSwap               uint64
	CNSwap              uint64
	ExitSignal          int32
	Processor           int32
	RTPriority          uint32
	Policy              uint32
	DelayAcctBLKIOTicks uint64
	GuestTime           uint64
	ChildrenGuestTIme   uint64
	StartData           uint64
	EndData             uint64
	StartBrk            uint64
	ArgStart            uint64
	ArgEnd              uint64
	EnvStart            uint64
	EnvEnd              uint64
	ExitCode            int32
}

// String returns JSON value of the process.
func (m MemoryMapsStat) String() string {
	s, _ := json.Marshal(m)
	return string(s)
}

// NewProcess creates a new Process instance, it only stores the pid and
// checks that the process exists. Other method on Process can be used
// to get more information about the process. An error will be returned
// if the process does not exist.
func NewProcess(pid int32) (*Process, error) {
	p := &Process{
		Pid: int32(pid),
	}
	file, err := os.Open(common.HostProc(strconv.Itoa(int(p.Pid))))
	defer file.Close()
	return p, err
}

func (p *Process) GetStats() (*ProcStat, error) {
	err := p.fillFromStat()
	if err != nil {
		return nil, err
	}
	return p.stats, nil
}

// Ppid returns Parent Process ID of the process.
func (p *Process) Ppid() (int32, error) {
	err := p.fillFromStat()
	if err != nil {
		return -1, err
	}
	return p.stats.ParentPID, nil
}

// Name returns name of the process.
func (p *Process) Name() (string, error) {
	if p.name == "" {
		if err := p.fillFromStatus(); err != nil {
			return "", err
		}
	}
	return p.name, nil
}

// Exe returns executable path of the process.
func (p *Process) Exe() (string, error) {
	return p.fillFromExe()
}

// Cmdline returns the command line arguments of the process as a string with
// each argument separated by 0x20 ascii character.
func (p *Process) Cmdline() (string, error) {
	return p.fillFromCmdline()
}

// CmdlineSlice returns the command line arguments of the process as a slice with each
// element being an argument.
func (p *Process) CmdlineSlice() ([]string, error) {
	return p.fillSliceFromCmdline()
}

// CreateTime returns created time of the process in seconds since the epoch, in UTC.
func (p *Process) CreateTime() (int64, error) {
	err := p.fillFromStat()
	if err != nil {
		return 0, err
	}
	return p.stats.StartTime, nil
}

// Cwd returns current working directory of the process.
func (p *Process) Cwd() (string, error) {
	return p.fillFromCwd()
}

// Parent returns parent Process of the process.
func (p *Process) Parent() (*Process, error) {
	err := p.fillFromStatus()
	if err != nil {
		return nil, err
	}
	if p.parent == 0 {
		return nil, fmt.Errorf("wrong number of parents")
	}
	return NewProcess(p.parent)
}

// Status returns the process status.
// Return value could be one of these.
// R: Running S: Sleep T: Stop I: Idle
// Z: Zombie W: Wait L: Lock
// The charactor is same within all supported platforms.
func (p *Process) Status() (string, error) {
	err := p.fillFromStatus()
	if err != nil {
		return "", err
	}
	return p.status, nil
}

// Uids returns user ids of the process as a slice of the int
func (p *Process) Uids() ([]int32, error) {
	err := p.fillFromStatus()
	if err != nil {
		return []int32{}, err
	}
	return p.uids, nil
}

// Gids returns group ids of the process as a slice of the int
func (p *Process) Gids() ([]int32, error) {
	err := p.fillFromStatus()
	if err != nil {
		return []int32{}, err
	}
	return p.gids, nil
}

// Terminal returns a terminal which is associated with the process.
func (p *Process) Terminal() (int64, error) {
	err := p.fillFromStat()
	if err != nil {
		return 0, err
	}
	return p.stats.TTY, nil
}

// Nice returns a nice value (priority).
// Notice: gopsutil can not set nice value.
func (p *Process) Nice() (int32, error) {
	err := p.fillFromStat()
	if err != nil {
		return 0, err
	}
	return p.stats.Nice, nil
}

// IOnice returns process I/O nice value (priority).
func (p *Process) IOnice() (int32, error) {
	return 0, common.ErrNotImplementedError
}

// Rlimit returns Resource Limits.
func (p *Process) Rlimit() ([]RlimitStat, error) {
	return nil, common.ErrNotImplementedError
}

// IOCounters returns IO Counters.
func (p *Process) IOCounters() (*IOCountersStat, error) {
	return p.fillFromIO()
}

// NumCtxSwitches returns the number of the context switches of the process.
func (p *Process) NumCtxSwitches() (*NumCtxSwitchesStat, error) {
	err := p.fillFromStatus()
	if err != nil {
		return nil, err
	}
	return p.numCtxSwitches, nil
}

// NumFDs returns the number of File Descriptors used by the process.
func (p *Process) NumFDs() (int32, error) {
	numFds, _, err := p.fillFromfd()
	return numFds, err
}

// NumThreads returns the number of threads used by the process.
func (p *Process) NumThreads() (int32, error) {
	err := p.fillFromStatus()
	if err != nil {
		return 0, err
	}
	return p.numThreads, nil
}

// Threads returns a map of threads
//
// Notice: Not implemented yet. always returns empty map.
func (p *Process) Threads() (map[string]string, error) {
	ret := make(map[string]string, 0)
	return ret, nil
}

// Times returns CPU times of the process.
func (p *Process) Times() (*cpu.TimesStat, error) {
	err := p.fillFromStat()
	if err != nil {
		return nil, err
	}
	cpuTimes := &cpu.TimesStat{
		CPU:    "cpu",
		User:   float64(p.stats.UserTime / ClockTicks),
		System: float64(p.stats.SystemTime / ClockTicks),
	}
	return cpuTimes, nil
}

// CPUAffinity returns CPU affinity of the process.
//
// Notice: Not implemented yet.
func (p *Process) CPUAffinity() ([]int32, error) {
	return nil, common.ErrNotImplementedError
}

// MemoryInfo returns platform in-dependend memory information, such as RSS, VMS and Swap
func (p *Process) MemoryInfo() (*MemoryInfoStat, error) {
	meminfo, _, err := p.fillFromStatm()
	if err != nil {
		return nil, err
	}
	return meminfo, nil
}

// MemoryInfoEx returns platform dependend memory information.
func (p *Process) MemoryInfoEx() (*MemoryInfoExStat, error) {
	_, memInfoEx, err := p.fillFromStatm()
	if err != nil {
		return nil, err
	}
	return memInfoEx, nil
}

// Children returns a slice of Process of the process.
func (p *Process) Children() ([]*Process, error) {
	pids, err := common.CallPgrep(invoke, p.Pid)
	if err != nil {
		if pids == nil || len(pids) == 0 {
			return nil, ErrorNoChildren
		}
		return nil, err
	}
	ret := make([]*Process, 0, len(pids))
	for _, pid := range pids {
		np, err := NewProcess(pid)
		if err != nil {
			return nil, err
		}
		ret = append(ret, np)
	}
	return ret, nil
}

// OpenFiles returns a slice of OpenFilesStat opend by the process.
// OpenFilesStat includes a file path and file descriptor.
func (p *Process) OpenFiles() ([]OpenFilesStat, error) {
	_, ofs, err := p.fillFromfd()
	if err != nil {
		return nil, err
	}
	ret := make([]OpenFilesStat, len(ofs))
	for i, o := range ofs {
		ret[i] = *o
	}

	return ret, nil
}

// Connections returns a slice of net.ConnectionStat used by the process.
// This returns all kind of the connection. This measn TCP, UDP or UNIX.
func (p *Process) Connections() ([]net.ConnectionStat, error) {
	return net.ConnectionsPid("all", p.Pid)
}

// NetIOCounters returns NetIOCounters of the process.
func (p *Process) NetIOCounters(pernic bool) ([]net.IOCountersStat, error) {
	filename := common.HostProc(strconv.Itoa(int(p.Pid)), "net/dev")
	return net.IOCountersByFile(pernic, filename)
}

// IsRunning returns whether the process is running or not.
// Not implemented yet.
func (p *Process) IsRunning() (bool, error) {
	return true, common.ErrNotImplementedError
}

// MemoryMaps get memory maps from /proc/(pid)/smaps
func (p *Process) MemoryMaps(grouped bool) (*[]MemoryMapsStat, error) {
	pid := p.Pid
	var ret []MemoryMapsStat
	smapsPath := common.HostProc(strconv.Itoa(int(pid)), "smaps")
	contents, err := ioutil.ReadFile(smapsPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(contents), "\n")

	// function of parsing a block
	getBlock := func(first_line []string, block []string) (MemoryMapsStat, error) {
		m := MemoryMapsStat{}
		m.Path = first_line[len(first_line)-1]

		for _, line := range block {
			if strings.Contains(line, "VmFlags") {
				continue
			}
			field := strings.Split(line, ":")
			if len(field) < 2 {
				continue
			}
			v := strings.Trim(field[1], " kB") // remove last "kB"
			t, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return m, err
			}

			switch field[0] {
			case "Size":
				m.Size = t
			case "Rss":
				m.Rss = t
			case "Pss":
				m.Pss = t
			case "Shared_Clean":
				m.SharedClean = t
			case "Shared_Dirty":
				m.SharedDirty = t
			case "Private_Clean":
				m.PrivateClean = t
			case "Private_Dirty":
				m.PrivateDirty = t
			case "Referenced":
				m.Referenced = t
			case "Anonymous":
				m.Anonymous = t
			case "Swap":
				m.Swap = t
			}
		}
		return m, nil
	}

	blocks := make([]string, 16)
	for _, line := range lines {
		field := strings.Split(line, " ")
		if strings.HasSuffix(field[0], ":") == false {
			// new block section
			if len(blocks) > 0 {
				g, err := getBlock(field, blocks)
				if err != nil {
					return &ret, err
				}
				ret = append(ret, g)
			}
			// starts new block
			blocks = make([]string, 16)
		} else {
			blocks = append(blocks, line)
		}
	}

	return &ret, nil
}

/**
** Internal functions
**/

// Get num_fds from /proc/(pid)/fd
func (p *Process) fillFromfd() (int32, []*OpenFilesStat, error) {
	pid := p.Pid
	statPath := common.HostProc(strconv.Itoa(int(pid)), "fd")
	d, err := os.Open(statPath)
	if err != nil {
		return 0, nil, err
	}
	defer d.Close()
	fnames, err := d.Readdirnames(-1)
	numFDs := int32(len(fnames))

	var openfiles []*OpenFilesStat
	for _, fd := range fnames {
		fpath := filepath.Join(statPath, fd)
		filepath, err := os.Readlink(fpath)
		if err != nil {
			continue
		}
		t, err := strconv.ParseUint(fd, 10, 64)
		if err != nil {
			return numFDs, openfiles, err
		}
		o := &OpenFilesStat{
			Path: filepath,
			Fd:   t,
		}
		openfiles = append(openfiles, o)
	}

	return numFDs, openfiles, nil
}

// Get cwd from /proc/(pid)/cwd
func (p *Process) fillFromCwd() (string, error) {
	pid := p.Pid
	cwdPath := common.HostProc(strconv.Itoa(int(pid)), "cwd")
	cwd, err := os.Readlink(cwdPath)
	if err != nil {
		return "", err
	}
	return string(cwd), nil
}

// Get exe from /proc/(pid)/exe
func (p *Process) fillFromExe() (string, error) {
	pid := p.Pid
	exePath := common.HostProc(strconv.Itoa(int(pid)), "exe")
	exe, err := os.Readlink(exePath)
	if err != nil {
		return "", err
	}
	return string(exe), nil
}

// Get cmdline from /proc/(pid)/cmdline
func (p *Process) fillFromCmdline() (string, error) {
	pid := p.Pid
	cmdPath := common.HostProc(strconv.Itoa(int(pid)), "cmdline")
	cmdline, err := ioutil.ReadFile(cmdPath)
	if err != nil {
		return "", err
	}
	ret := strings.FieldsFunc(string(cmdline), func(r rune) bool {
		if r == '\u0000' {
			return true
		}
		return false
	})

	return strings.Join(ret, " "), nil
}

func (p *Process) fillSliceFromCmdline() ([]string, error) {
	pid := p.Pid
	cmdPath := common.HostProc(strconv.Itoa(int(pid)), "cmdline")
	cmdline, err := ioutil.ReadFile(cmdPath)
	if err != nil {
		return nil, err
	}
	if len(cmdline) == 0 {
		return nil, nil
	}
	if cmdline[len(cmdline)-1] == 0 {
		cmdline = cmdline[:len(cmdline)-1]
	}
	parts := bytes.Split(cmdline, []byte{0})
	var strParts []string
	for _, p := range parts {
		strParts = append(strParts, string(p))
	}

	return strParts, nil
}

// Get IO status from /proc/(pid)/io
func (p *Process) fillFromIO() (*IOCountersStat, error) {
	pid := p.Pid
	ioPath := common.HostProc(strconv.Itoa(int(pid)), "io")
	ioline, err := ioutil.ReadFile(ioPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(ioline), "\n")
	ret := &IOCountersStat{}

	for _, line := range lines {
		field := strings.Fields(line)
		if len(field) < 2 {
			continue
		}
		t, err := strconv.ParseUint(field[1], 10, 64)
		if err != nil {
			return nil, err
		}
		param := field[0]
		if strings.HasSuffix(param, ":") {
			param = param[:len(param)-1]
		}
		switch param {
		case "syscr":
			ret.ReadCount = t
		case "syscw":
			ret.WriteCount = t
		case "read_bytes":
			ret.ReadBytes = t
		case "write_bytes":
			ret.WriteBytes = t
		}
	}

	return ret, nil
}

// Get memory info from /proc/(pid)/statm
func (p *Process) fillFromStatm() (*MemoryInfoStat, *MemoryInfoExStat, error) {
	pid := p.Pid
	memPath := common.HostProc(strconv.Itoa(int(pid)), "statm")
	contents, err := ioutil.ReadFile(memPath)
	if err != nil {
		return nil, nil, err
	}
	fields := strings.Split(string(contents), " ")

	vms, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return nil, nil, err
	}
	rss, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return nil, nil, err
	}
	memInfo := &MemoryInfoStat{
		RSS: rss * PageSize,
		VMS: vms * PageSize,
	}

	shared, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return nil, nil, err
	}
	text, err := strconv.ParseUint(fields[3], 10, 64)
	if err != nil {
		return nil, nil, err
	}
	lib, err := strconv.ParseUint(fields[4], 10, 64)
	if err != nil {
		return nil, nil, err
	}
	dirty, err := strconv.ParseUint(fields[5], 10, 64)
	if err != nil {
		return nil, nil, err
	}

	memInfoEx := &MemoryInfoExStat{
		RSS:    rss * PageSize,
		VMS:    vms * PageSize,
		Shared: shared * PageSize,
		Text:   text * PageSize,
		Lib:    lib * PageSize,
		Dirty:  dirty * PageSize,
	}

	return memInfo, memInfoEx, nil
}

func (p *Process) VirtualMemoryInfo() (*VirtualMemoryStat, error) {
	if p.virtualMemInfo == nil {
		err := p.fillFromStat()
		if err != nil {
			return nil, err
		}
	}
	return p.virtualMemInfo, nil
}

// Get various status from /proc/(pid)/status
func (p *Process) fillFromStatus() error {
	// If we already gathered the info, avoid opening/reading/closing the file again
	if p.virtualMemInfo != nil {
		return nil
	}
	pid := p.Pid
	statPath := common.HostProc(strconv.Itoa(int(pid)), "status")
	contents, err := ioutil.ReadFile(statPath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(contents), "\n")
	p.numCtxSwitches = &NumCtxSwitchesStat{}
	p.memInfo = &MemoryInfoStat{}
	p.virtualMemInfo = &VirtualMemoryStat{}
	for _, line := range lines {
		tabParts := strings.SplitN(line, "\t", 2)
		if len(tabParts) < 2 {
			continue
		}
		value := tabParts[1]
		switch strings.TrimRight(tabParts[0], ":") {
		case "Name":
			p.name = strings.Trim(value, " \t")
		case "State":
			p.status = value[0:1]
		case "PPid", "Ppid":
			pval, err := strconv.ParseInt(value, 10, 32)
			if err != nil {
				return err
			}
			p.parent = int32(pval)
		case "Uid":
			p.uids = make([]int32, 0, 4)
			for _, i := range strings.Split(value, "\t") {
				v, err := strconv.ParseInt(i, 10, 32)
				if err != nil {
					return err
				}
				p.uids = append(p.uids, int32(v))
			}
		case "Gid":
			p.gids = make([]int32, 0, 4)
			for _, i := range strings.Split(value, "\t") {
				v, err := strconv.ParseInt(i, 10, 32)
				if err != nil {
					return err
				}
				p.gids = append(p.gids, int32(v))
			}
		case "Threads":
			v, err := strconv.ParseInt(value, 10, 32)
			if err != nil {
				return err
			}
			p.numThreads = int32(v)
		case "voluntary_ctxt_switches":
			v, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			p.numCtxSwitches.Voluntary = v
		case "nonvoluntary_ctxt_switches":
			v, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			p.numCtxSwitches.Involuntary = v
		case "VmRSS":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.memInfo.RSS = v * 1024
			p.virtualMemInfo.VMRSS = v * 1024
		case "VmSize":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.memInfo.VMS = v * 1024
			p.virtualMemInfo.VMSize = v * 1024
		case "VmSwap":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.memInfo.Swap = v * 1024
			//
		case "VmPeak":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.virtualMemInfo.VMPeak = v * 1024
		case "VmLck":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.virtualMemInfo.VMLck = v * 1024
		case "VmPin":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.virtualMemInfo.VMPin = v * 1024
		case "VmHWM":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.virtualMemInfo.VMHWM = v * 1024
		case "VmData":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.virtualMemInfo.VMData = v * 1024
		case "VmStk":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.virtualMemInfo.VMStk = v * 1024
		case "VmExe":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.virtualMemInfo.VMExe = v * 1024
		case "VmLib":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.virtualMemInfo.VMLib = v * 1024

		case "VmPTE":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.virtualMemInfo.VMPTE = v * 1024

		case "VmPMD":
			value := strings.Trim(value, " kB") // remove last "kB"
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			p.virtualMemInfo.VMPMD = v * 1024

		}

	}
	return nil
}

func (p *Process) fillFromStat() error {
	if p.stats != nil {
		return nil
	}
	pid := p.Pid

	statPath := common.HostProc(strconv.Itoa(int(pid)), "stat")
	inputFile, err := os.Open(statPath)
	if err != nil {
		return err
	}
	if p.stats == nil {
		p.stats = &ProcStat{}
	}
	var o1, o2, o3 uint64 // obsolete fields but we need to scan them
	_, err = fmt.Fscanf(inputFile, "%d %s %s %d %d %d %d %d %d %d "+
		"%d %d %d %f %f %f %f %d %d %d "+
		"%d %d %d %d %s %d %d %d %d %d "+
		"%d %d %d %d %d %d %d %d %d %d "+
		"%d %d %d %d %d %d %d %d %d %d "+
		"%d %d\n",
		&p.stats.PID,
		&p.stats.Name,
		&p.stats.State,
		&p.stats.ParentPID,
		&p.stats.ProcessGroupID,
		&p.stats.SessionID,
		&p.stats.TTY,
		&p.stats.ForegroundProcessGroupID,
		&p.stats.Flags,
		&p.stats.MinorPageFaults,
		&p.stats.ChildrenMinorPageFaults,
		&p.stats.MajorPageFaults,
		&p.stats.ChildreMajorPageFaults,
		&p.stats.UserTime,
		&p.stats.SystemTime,
		&p.stats.ChildrenUserTime,
		&p.stats.ChildrenKernelTime,
		&p.stats.Priority,
		&p.stats.Nice,
		&p.stats.Threads,
		&p.stats.ITRealValue,
		&p.stats.StartTime,
		&p.stats.VSize,
		&p.stats.RSS,
		&p.stats.RSSLim,
		&p.stats.StartCode,
		&p.stats.EndCode,
		&p.stats.StartStack,
		&p.stats.KstkESP,
		&p.stats.KstkIP,
		&p.stats.Signal,
		&o1,
		&o2,
		&o3,
		&p.stats.WChan,
		&p.stats.NSwap,
		&p.stats.CNSwap,
		&p.stats.ExitSignal,
		&p.stats.Processor,
		&p.stats.RTPriority,
		&p.stats.Policy,
		&p.stats.DelayAcctBLKIOTicks,
		&p.stats.GuestTime,
		&p.stats.ChildrenGuestTIme,
		&p.stats.StartData,
		&p.stats.EndData,
		&p.stats.StartBrk,
		&p.stats.ArgStart,
		&p.stats.ArgEnd,
		&p.stats.EnvStart,
		&p.stats.EnvEnd,
		&p.stats.ExitCode,
	)
	inputFile.Close()
	if err != nil {
		return err
	}

	p.stats.UserTime /= ClockTicks
	p.stats.SystemTime /= ClockTicks
	p.stats.ChildrenUserTime /= ClockTicks
	p.stats.ChildrenKernelTime /= ClockTicks
	p.stats.StartTime /= ClockTicks
	p.stats.Name = strings.TrimPrefix(p.stats.Name, "(")
	p.stats.Name = strings.TrimSuffix(p.stats.Name, ")")

	return nil
}

// Pids returns a slice of process ID list which are running now.
func Pids() ([]int32, error) {
	var ret []int32

	d, err := os.Open(common.HostProc())
	if err != nil {
		return nil, err
	}
	defer d.Close()

	fnames, err := d.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	for _, fname := range fnames {
		pid, err := strconv.ParseInt(fname, 10, 32)
		if err != nil {
			// if not numeric name, just skip
			continue
		}
		ret = append(ret, int32(pid))
	}

	return ret, nil
}