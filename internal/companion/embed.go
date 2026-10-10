// Package companion 装着面板自带的那个 Bukkit 插件。
//
// 它存在的唯一理由：服务端内存里的东西（玩家坐标、背包、血量）不在任何文件里，
// 光靠读盘永远读不到。插件是一个**纯观察者** —— 不监听任何事件，只定时把状态写成文件。
// 之所以敢做成「往用户服务器里塞一个 jar」，是因为它的冲突面被压到了接近零：
// 不抢事件、不改行为、不注册命令，玩家在 /help 里看不到它。
//
// 源码在 tools/companion/，改完跑 `bash tools/companion/build.sh` 重新生成这里的 jar。
package companion

import _ "embed"

//go:embed EnderBoxCompanion.jar
var Jar []byte

// FileName 落盘时用的文件名。服务端按这个名字建数据目录，改了两边就对不上。
const FileName = "EnderBoxCompanion.jar"

// PluginName 服务端里插件的名字 = 数据目录名。
const PluginName = "EnderBoxCompanion"

// Version 插件版本。写在它自己输出的快照里，用来判断「用户服务器上的那份是旧的」。
const Version = "0.2.0"

// Bridge 插件数据目录下的桥接目录名。
const Bridge = "bridge"

// BridgeRel 从实例目录算起的相对路径。面板和插件必须算出同一个目录。
var BridgeRel = []string{"plugins", PluginName, Bridge}
