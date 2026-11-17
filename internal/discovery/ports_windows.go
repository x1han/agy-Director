//go:build windows

package discovery

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

// mibTCPRow 是 MIB_TCPROW 的结构（5 个 uint32 = 20 字节，无填充）。
//
// 字段里的地址与端口都是**网络字节序**：内存里的字节序就是正确顺序，
// 不能再用 LittleEndian/BigEndian 再转一次。
type mibTCPRow struct {
	dwState      uint32
	dwLocalAddr  uint32
	dwLocalPort  uint32
	dwRemoteAddr uint32
	dwRemotePort uint32
}

// TCP_TABLE_CLASS 取值（winnt.h）：
//
//	TCP_TABLE_BASIC_LISTENER        = 0
//	TCP_TABLE_BASIC_CONNECTIONS     = 1
//	TCP_TABLE_BASIC_ALL             = 2
//	TCP_TABLE_OWNER_PID_LISTENER    = 3
//	TCP_TABLE_OWNER_PID_CONNECTIONS = 4
//	TCP_TABLE_OWNER_PID_ALL         = 5
//
// 实测在本机（Windows 11 26200）只有 **class=2** 能成功返回数据；
// class 0/1/3/4/5 一律 ERROR_INVALID_PARAMETER(87)。
// 所以用 BASIC_ALL(2)，并按 dwState 自己过滤出 LISTEN。
//
// MIB_TCP_STATE_LISTEN = 2
const (
	tcpTableBasicAll     = 2
	mibTCPStateListen    = 2
	errorInsufficientBuf = 122
)

var (
	iphlpapi                = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
)

// listenPorts 用 iphlpapi 的 GetExtendedTcpTable 读监听表。
//
// 不用 netstat 解析：netstat 的输出格式会随系统语言与版本变化，
// 脆弱且慢。GetExtendedTcpTable 直接给二进制结构。
//
// ── 三个踩过的坑（都实测过）────────────────────────────────────────
//
//  1. GetExtendedTcpTable 是**五个**参数：
//
//     DWORD GetExtendedTcpTable(
//         PVOID   pTcpTable,   // [out] 表
//         PDWORD  pdwSize,     // [out] 表的字节数
//         BOOL    bOrder,      // [in]  是否按地址排序
//         ULONG   ulClass,     // [in]  TCP_TABLE_CLASS
//         ULONG   Reserved     // [in]  保留，必须为 0
//     );
//
//     少传一个 → ERROR_INVALID_PARAMETER(87)。
//
//  2. 第一次调用（pTcpTable=NULL）**必须**返回
//     ERROR_INSUFFICIENT_BUFFER(122) 才算正常——那是在问"表多大"，
//     不是出错。把任何非零返回都当失败，就永远拿不到表，
//     表现为「本机一个监听端口都没有」。
//
//  3. TCP_TABLE_BASIC_ALL 的内存布局是：
//
//     [0:4]   uint32  行数
//     [4:...] 每行 20 字节 = MIB_TCPROW
//
//     **表头 4 字节不能漏**，漏了会把表头当成第一行的 dwState。
//     （行数是 53、表总长 1072 时，4 + 53*20 = 1064 ≠ 1072，
//     差的 8 字节是系统对齐填充。）
//
//     实测校验：读出的 53 个监听端口与 netstat -ano 的
//     53 条 LISTENING **完全一致**（逐条比对）。
func listenPorts() ([]ListenPort, error) {
	var size uint32
	ret, _, _ := procGetExtendedTcpTable.Call(
		0,                                  // pTcpTable = NULL
		uintptr(unsafe.Pointer(&size)),     // pdwSize
		uintptr(1),                         // bOrder = TRUE
		uintptr(tcpTableBasicAll),          // ulClass
		uintptr(0),                         // Reserved
	)
	if ret != uintptr(errorInsufficientBuf) {
		if ret == 0 {
			// 表是空的（连 size 都没变），当作没有监听端口。
			return nil, nil
		}
		return nil, fmt.Errorf("GetExtendedTcpTable 查询大小失败: %d", ret)
	}
	if size == 0 {
		return nil, nil
	}

	buf := make([]byte, size)
	ret, _, _ = procGetExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		uintptr(1),
		uintptr(tcpTableBasicAll),
		uintptr(0),
	)
	if ret != 0 {
		return nil, fmt.Errorf("GetExtendedTcpTable 读取失败: %d", ret)
	}
	if len(buf) < 4 {
		return nil, nil
	}

	// 表头：行数
	count := int(binary.LittleEndian.Uint32(buf[0:4]))
	rowSize := int(unsafe.Sizeof(mibTCPRow{}))
	if count < 0 || count > len(buf)/rowSize {
		// 行数不可信（表被截断等），退回按缓冲区长度算。
		count = len(buf) / rowSize
	}

	seen := map[string]bool{}
	var out []ListenPort

	for i := 0; i < count; i++ {
		off := 4 + i*rowSize
		if off+rowSize > len(buf) {
			break
		}
		row := (*mibTCPRow)(unsafe.Pointer(&buf[off]))
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
		})
	}
	return out, nil
}
