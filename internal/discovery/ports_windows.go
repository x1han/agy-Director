//go:build windows

package discovery

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

// ── Win32 API 契约 ───────────────────────────────────────────────────
//
// MSDN 上的标准 C 签名是 **6 个**参数：
//
//	DWORD GetExtendedTcpTable(
//	    PVOID           pTcpTable,  // [out] 表
//	    PDWORD          pdwSize,    // [out] 表的字节数
//	    BOOL            bOrder,     // [in]  是否按地址排序
//	    ULONG           ulAf,       // [in]  AF_INET(2) 或 AF_INET6(23)
//	    TCP_TABLE_CLASS TableClass, // [in]  枚举值
//	    ULONG           Reserved    // [in]  保留，必须为 0
//	);
//
// **第 4 个参数 ulAf 不能省。** 少传它时，TableClass 会落到
// ulAf 的槽位、Reserved 落到 TableClass 的槽位，于是：
//   - 传 2 → 被当成 ulAf=AF_INET(2) + TableClass=0(BASIC_LISTENER)
//   - 传 0/1/3/4/5 → ulAf 非法 → ERROR_INVALID_PARAMETER(87)
//
// 这就是为什么"只传 2 时碰巧成功"——那不是 class=2 可用，
// 而是被解释成了 ulAf=AF_INET + TableClass=BASIC_LISTENER。
//
// TCP_TABLE_CLASS 取值：
//
//	TCP_TABLE_BASIC_LISTENER        = 0
//	TCP_TABLE_BASIC_CONNECTIONS     = 1
//	TCP_TABLE_BASIC_ALL             = 2
//	TCP_TABLE_OWNER_PID_LISTENER    = 3
//	TCP_TABLE_OWNER_PID_CONNECTIONS = 4
//	TCP_TABLE_OWNER_PID_ALL         = 5
//
// 用 **OWNER_PID_LISTENER(3)**：
//   - API 自己只返回 LISTEN 状态的行（不用再按 dwState 过滤）
//   - 每行带 dwOwningPid，能直接告诉用户"10808 是哪个进程开的"
//
// 实测（Windows 11 26200, IPv4）：
//
//	ulAf=2  class=3 → 1332 字节, 计数 55
//	ulAf=23 class=3 →  628 字节, 计数 11
//
// 与 netstat / Get-NetTCPConnection 逐条比对：**IPv4 55/55 地址与 PID 全对，
// IPv6 11/11 地址与 PID 全对**。

const (
	afInet  = 2  // AF_INET
	afInet6 = 23 // AF_INET6

	tableOwnerPIDListener = 3

	errorInsufficientBuf = 122 // ERROR_INSUFFICIENT_BUFFER

	// MIB_TCP_STATE_LISTEN = 2
	mibTCPStateListen = 2
)

// mibTCPRowOwnerPID 是 MIB_TCPROW_OWNER_PID。
//
// 布局（实测确认，24 字节）：
//
//	+00 dwState        (4)
//	+04 dwLocalAddr    (4)  IPv4，网络字节序
//	+08 dwLocalPort    (4)  端口在低 2 字节，网络字节序
//	+12 dwRemoteAddr   (4)
//	+16 dwRemotePort   (4)
//	+20 dwOwningPid    (4)
//
// 与 MSDN 的 MIB_TCPROW_OWNER_PID 定义一致。
type mibTCPRowOwnerPID struct {
	dwState      uint32
	dwLocalAddr  uint32
	dwLocalPort  uint32
	dwRemoteAddr uint32
	dwRemotePort uint32
	dwOwningPid  uint32
}

// mibTCP6RowOwnerPID 是 MIB_TCP6ROW_OWNER_PID。
//
// ⚠️ 布局与 IPv4 **完全不同**（实测确认，56 字节）：
//
//	+00 dwLocalAddr    (16) IPv6
//	+16 dwLocalScopeId (4)
//	+20 dwLocalPort    (4)  端口在低 2 字节，网络字节序
//	+24 dwRemoteAddr   (16)
//	+40 dwRemoteScopeId(4)
//	+44 dwRemotePort   (4)
//	+48 dwState        (4)
//	+52 dwOwningPid    (4)
//
// 三个要点：
//
//  1. **dwState 在行的末尾（+48），不在开头。** IPv6 版的成员顺序
//     与 IPv4 版不同；按 IPv4 的直觉去读会把状态读成 0。
//  2. 地址后面跟着 **ScopeId**（4 字节），端口在其后。
//     少算这 4 字节，端口就会读成地址的高位。
//  3. dwLocalAddr 是 **网络字节序**的 IPv6，直接 net.IP(...).String()
//     就是正确形式，不需要再转。
//
// 实测校验：与 Get-NetTCPConnection 的 11 条 IPv6 监听
// **地址与 PID 全部一致**。
type mibTCP6RowOwnerPID struct {
	dwLocalAddr     [16]byte
	dwLocalScopeId  uint32
	dwLocalPort     uint32
	dwRemoteAddr    [16]byte
	dwRemoteScopeId uint32
	dwRemotePort    uint32
	dwState         uint32
	dwOwningPid     uint32
}

var (
	iphlpapi                = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
)

// listenPorts 枚举本机所有 TCP 监听端口（IPv4 + IPv6）。
//
// 不用 netstat 解析：netstat 的输出格式随系统语言与版本变化，
// 且 netstat 默认**不显示 IPv6 监听**（要加 -p TCPv6），
// 解析很脆弱。GetExtendedTcpTable 直接给二进制结构。
func listenPorts() ([]ListenPort, error) {
	var out []ListenPort

	v4, err := fetchTCPTable(afInet)
	if err != nil {
		return nil, err
	}
	out = append(out, parseIPv4Rows(v4)...)

	// IPv6 单独取一次。失败不影响 IPv4 的结果（IPv6 监听是补充）。
	if v6, err := fetchTCPTable(afInet6); err == nil {
		out = append(out, parseIPv6Rows(v6)...)
	}

	return out, nil
}

// fetchTCPTable 取一张 TCP 表的原始字节。
//
// 关键：第一次调用（pTcpTable=NULL）**必须**返回
// ERROR_INSUFFICIENT_BUFFER(122) 才算正常——那是在问"表多大"，
// 不是出错。把任何非零返回都当失败，就永远拿不到表。
func fetchTCPTable(af uintptr) ([]byte, error) {
	var size uint32
	ret, _, _ := procGetExtendedTcpTable.Call(
		0,                              // pTcpTable = NULL
		uintptr(unsafe.Pointer(&size)), // pdwSize
		uintptr(1),                     // bOrder = TRUE
		af,                             // ulAf
		uintptr(tableOwnerPIDListener), // TableClass
		uintptr(0),                     // Reserved
	)
	if ret != uintptr(errorInsufficientBuf) {
		if ret == 0 {
			return nil, nil // 空表
		}
		return nil, fmt.Errorf("GetExtendedTcpTable(af=%d) 查询大小失败: %d", af, ret)
	}
	if size == 0 {
		return nil, nil
	}

	buf := make([]byte, size)
	ret, _, _ = procGetExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		uintptr(1),
		af,
		uintptr(tableOwnerPIDListener),
		uintptr(0),
	)
	if ret != 0 {
		return nil, fmt.Errorf("GetExtendedTcpTable(af=%d) 读取失败: %d", af, ret)
	}
	return buf, nil
}

// parseIPv4Rows 解析 IPv4 表。
//
// 表布局：4 字节行数头，之后每行 24 字节（MIB_TCPROW_OWNER_PID）。
// 行数用表头的值，不用缓冲区长度推算——后者会被对齐填充搞错。
func parseIPv4Rows(buf []byte) []ListenPort {
	if len(buf) < 4 {
		return nil
	}
	count := int(binary.LittleEndian.Uint32(buf[0:4]))
	rowSize := int(unsafe.Sizeof(mibTCPRowOwnerPID{}))
	if count < 0 || count > (len(buf)-4)/rowSize {
		count = (len(buf) - 4) / rowSize
	}

	seen := map[string]bool{}
	var out []ListenPort

	for i := 0; i < count; i++ {
		off := 4 + i*rowSize
		if off+rowSize > len(buf) {
			break
		}
		row := (*mibTCPRowOwnerPID)(unsafe.Pointer(&buf[off]))

		// OWNER_PID_LISTENER 只返回 LISTEN 行，但仍然核对状态。
		if row.dwState != mibTCPStateListen {
			continue
		}

		// dwLocalAddr 的 4 个字节就是网络序的 IPv4
		ipBytes := (*[4]byte)(unsafe.Pointer(&row.dwLocalAddr))
		host, wildcard := normalizeListenHost(net.IP(ipBytes[:]).String())

		// dwLocalPort 的低 2 字节是网络序端口
		portBytes := (*[2]byte)(unsafe.Pointer(&row.dwLocalPort))
		port := int(binary.BigEndian.Uint16(portBytes[:]))

		key := fmt.Sprintf("%s:%d", host, port)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ListenPort{
			Host:     host,
			Port:     port,
			Wildcard: wildcard,
			PID:      int(row.dwOwningPid),
		})
	}
	return out
}

// parseIPv6Rows 解析 IPv6 表。
//
// 表布局：4 字节行数头，之后每行 56 字节（MIB_TCP6ROW_OWNER_PID）。
// 注意 dwState 在行的**末尾**（+48），不是开头。
func parseIPv6Rows(buf []byte) []ListenPort {
	if len(buf) < 4 {
		return nil
	}
	count := int(binary.LittleEndian.Uint32(buf[0:4]))
	rowSize := int(unsafe.Sizeof(mibTCP6RowOwnerPID{}))
	if count < 0 || count > (len(buf)-4)/rowSize {
		count = (len(buf) - 4) / rowSize
	}

	seen := map[string]bool{}
	var out []ListenPort

	for i := 0; i < count; i++ {
		off := 4 + i*rowSize
		if off+rowSize > len(buf) {
			break
		}
		row := (*mibTCP6RowOwnerPID)(unsafe.Pointer(&buf[off]))

		if row.dwState != mibTCPStateListen {
			continue
		}

		host, wildcard := normalizeListenHost(net.IP(row.dwLocalAddr[:]).String())

		portBytes := (*[2]byte)(unsafe.Pointer(&row.dwLocalPort))
		port := int(binary.BigEndian.Uint16(portBytes[:]))
		if port == 0 {
			continue
		}

		key := fmt.Sprintf("%s:%d", host, port)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ListenPort{
			Host:     host,
			Port:     port,
			Wildcard: wildcard,
			PID:      int(row.dwOwningPid),
		})
	}
	return out
}
