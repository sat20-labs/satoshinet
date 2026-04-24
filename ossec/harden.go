package ossec

import (
	"fmt"
	"io/ioutil"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
    SYS_PRCTL           = 157 // x86_64
    PR_SET_DUMPABLE     = 4
    PR_SET_PTRACER      = 0x59616d61
    PR_SET_PTRACER_ANY  = ^uintptr(0) // -1
    PR_SET_NO_NEW_PRIVS = 38

	RequireCoreDumpOff = true
	RequirePtraceScope3 = true
	RequireNoSwap = true
)

func prctl(option, arg2, arg3, arg4, arg5 uintptr) error {
    _, _, errno := syscall.Syscall6(
        SYS_PRCTL,
        option,
        arg2,
        arg3,
        arg4,
        arg5,
        0,
    )
    if errno != 0 {
        return errno
    }
    return nil
}

func Init() error {
	// 1. 基础检查 需要作为 systemd 管理，才有效
	// if err := basicChecks(); err != nil {
	// 	return err
	// }

	// 2. 进程级保护
	if err := hardenProcess(); err != nil {
		return err
	}

	return nil
}

func basicChecks() error {

/* TODO .service 文件必须设置如下参数, 同时确保只用signer用户运行
[Service]
User=signer
Group=signer

# 禁止 core dump
LimitCORE=0

# 内存锁定
LimitMEMLOCK=infinity

# 安全隔离
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes

# 限制能力
CapabilityBoundingSet=CAP_IPC_LOCK
*/
	// 1. core dump 检查
	if RequireCoreDumpOff {
		var rlim unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_CORE, &rlim); err != nil {
			return err
		}
		if rlim.Cur != 0 {
			return fmt.Errorf("core dump is not disabled (ulimit -c != 0)")
		}
	}

	// 2. ptrace_scope 检查
	if RequirePtraceScope3 {
		data, err := ioutil.ReadFile("/proc/sys/kernel/yama/ptrace_scope")
		if err != nil {
			return err
		}
		val := strings.TrimSpace(string(data))
		if val != "3" {
			return fmt.Errorf("ptrace_scope is %s, require 3", val)
		}
	}

	// 3. swap 检查
	if RequireNoSwap {
		data, err := ioutil.ReadFile("/proc/swaps")
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" {
			return fmt.Errorf("swap is enabled")
		}
	}

	// 4. root 运行检查（建议不要 root）
	// if RequireRootless {
	// 	if os.Geteuid() == 0 {
	// 		return fmt.Errorf("must not run as root")
	// 	}
	// }

	return nil
}

func hardenProcess() error {
	// 禁止进程被 dump
	if err := prctl(PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("failed to disable dumpable: %w", err)
	}

	// 2. 禁止提权 
    if err := prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
        return err
    }

    // 3. 锁内存
    if err := unix.Mlockall(unix.MCL_CURRENT | unix.MCL_FUTURE); err != nil {
        return err
    }

	return nil
}
