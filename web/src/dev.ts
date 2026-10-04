/**
 * 截图 / 调试开关。全部由 URL 决定，不带参数时行为为零。
 *
 * 为什么需要它们：无头浏览器点不了按钮，也没有「真实慢网络」这种东西，
 * 所以加载态和崩溃态根本截不出来核对。把状态放进 URL 是成本最低的通道 ——
 * 和已有的 ?nosse=1（不建 SSE）、?probe=1（溢出探针）、
 * ?drawer=1 / ?restore=1（预置交互态）是同一个套路。
 */
const P = new URLSearchParams(location.search)

/* ?slow=1200        —— 所有接口都延迟 1.2 秒
   ?slow=8000:plugins —— 只延迟路径含 plugins 的接口
   第二种是必需的：列表骨架必须先拿到实例列表才看得到（没有实例就没有插件页），
   全局延迟会把两件事一起拖住，结果只能截到首屏连接态。 */
const SLOW_RAW = P.get('slow') ?? ''
const [SLOW_MS_RAW, SLOW_MATCH = ''] = SLOW_RAW.split(':')
export const SLOW_MS = Number(SLOW_MS_RAW) || 0

/** ?crash=1 —— 让页面主体里抛一个异常，用来看错误边界。只在截图时用。 */
export const CRASH = P.has('crash')

/** 接口前的可选延迟。没带 ?slow= 时是空路径，正常使用零开销。 */
export function maybeDelay(path: string): Promise<void> {
  if (SLOW_MS <= 0) return Promise.resolve()
  if (SLOW_MATCH && !path.includes(SLOW_MATCH)) return Promise.resolve()
  return new Promise((r) => setTimeout(r, SLOW_MS))
}
