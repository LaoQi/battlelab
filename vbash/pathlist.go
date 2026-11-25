package vbash

import "strings"

// PathList 是 PATH 的语义视图：每个元素是**内部形态**的一段路径
// （/c/tools、bin），不变式是段内不含 ':'。
//
// 为什么不把数组当作变量真相：PATH 在 shell 里是可被任意文本操作的字符串
// （${PATH%:*}、PATH=$PATH:x、export 列表、子进程环境），一旦把数组当权威
// 状态，每次文本操作都要反序列化/再序列化。这里保持 Var.Value 为真相，
// PathList 只在消费点（查找、出站环境）解析出来使用。
type PathList []string

// ParsePathList 把 PATH 字符串切成内部形态的段数组。
// 切分规则由 plat.SplitPath 决定（Windows 段首盘符感知，其余平台按 ':'）。
// 段内的宿主形态由 plat.FromHost 归一，例如 Windows 上 D:/a 变 /d/a。
func ParsePathList(s string) PathList {
	parts := plat.SplitPath(s)
	out := make(PathList, len(parts))
	for i, p := range parts {
		out[i] = plat.FromHost(p)
	}
	return out
}

// String 序列化成内部形态（':' 连接），供 $PATH 展开、export/set 列表使用。
func (p PathList) String() string {
	return strings.Join(p, ":")
}

// Internal 返回内部形态的副本，供查找逻辑遍历。
func (p PathList) Internal() []string {
	return append([]string(nil), p...)
}

// Host 返回宿主形态数组，供子进程环境使用（调用方按 plat.PathSep 连接）。
func (p PathList) Host() []string {
	out := make([]string, len(p))
	for i, seg := range p {
		out[i] = plat.ToHost("", seg)
	}
	return out
}
