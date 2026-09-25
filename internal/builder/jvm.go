package builder

import (
	"os"
	"strconv"
	"strings"
)

// maxChildHeapMB 子 JVM（D8/R8/Kotlin 编译器）堆上限，按主机内存的一半估算并封顶 4GiB。
// 背景：build-tools 的 d8 脚本默认 -Xmx2G，在 16k 类规模下 GC 频繁，实测堆放宽后
// D8 从 ~171s 降到 ~114s。Kotlin 编译器同理需要足够堆。
func maxChildHeapMB() int {
	const capMB = 4096
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 2048
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		totalKB, err := strconv.Atoi(fields[1])
		if err != nil || totalKB <= 0 {
			break
		}
		heap := totalKB / 1024 / 2
		if heap > capMB {
			heap = capMB
		}
		if heap < 512 {
			heap = 512
		}
		return heap
	}
	return 2048
}

// jvmXmxFlag 返回形如 "-Xmx4096m" 的堆上限参数（供直接 java 调用使用）
func jvmXmxFlag() string {
	return "-Xmx" + strconv.Itoa(maxChildHeapMB()) + "m"
}

// d8JVMFlags 返回 d8 脚本可识别的 -J 前缀 JVM 参数
func d8JVMFlags() []string {
	return []string{"-JXmx" + strconv.Itoa(maxChildHeapMB()) + "m"}
}
