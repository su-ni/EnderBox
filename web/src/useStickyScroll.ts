/**
 * 「粘底」滚动 —— 新内容自动贴底、用户往回翻自动让路。
 *
 * 控制台大页（ConsolePage）和概览小窗（LogBody）共用这一份实现：
 * 两处以前各写各的、掉进过同一个坑（见下），修一处漏一处只是时间问题。
 *
 * ⚠⚠ 这个 hook 存在的**唯一原因**是修掉那个坑：
 *   用户实报「点『跟随』只跟一下下就断」。
 *   根因：程序贴底（scrollTop = scrollHeight）自己会触发 scroll 事件；事件派发前
 *   若新日志又把内容顶高一截（刷屏时必然发生），处理器算出的「离底 24px 外」
 *   被旧实现当成了「用户往回翻」→ 静默把跟随关掉。
 *   所以判定必须区分**谁在滚**：
 *     · scrollTop 与「程序最后贴到的位置」一致 → 是内容长高的回波，忽略；
 *     · 拖滚动条（wheel / touch 都不走）→ 靠 scrollTop 真变化兜底；
 *     · 滚轮 / 触摸有明确方向 → 直接判「用户在翻」，不等 scroll 事件。
 */

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type RefObject,
  type TouchEvent,
  type WheelEvent,
} from 'react'

export function useStickyScroll(
  ref: RefObject<HTMLDivElement | null>,
  /** 内容依赖：它一变（新日志进来）且跟随中 → 立刻贴底。通常传 blocks / entries。 */
  dep: unknown,
) {
  const [following, setFollowing] = useState(true)
  /** 判定用的真值。scroll 事件可能赶在 setState 提交前到达，判定必须同步、不能等渲染。 */
  const onRef = useRef(true)
  /** 程序贴底后 scrollTop 落到的像素位置。区分「回波」与「用户滚动」的锚。 */
  const stickyTop = useRef(0)
  /**
   * 最近一次「程序指派」是否还没认领过 scroll 事件（回波令牌）。
   * ⚠⚠ 存在的理由（2026-10-10 探针 T5 逮到）：原生 scroll 事件是**异步**派发的，
   *   我们写 scrollTop 后、事件到达前，用户完全可能先 peel 掉跟随 —— 此时回波事件
   *   姗姗来迟，若流到「gap<8 → stick()」恢复分支，就会把用户刚关掉的跟随**悄悄复活**
   *   （用户症状：点完跟随或日志刚刷新时往回滚，视图被弹回底部 =「滚不动」）。
   *   令牌 = 「位置还停在上次落点 + 该次指派尚未认领事件」→ 无论跟随开关，一律忽略。
   */
  const echoPending = useRef(false)
  /** 触摸屏上一指位置（判断手指是往下拖还是往上滑）。 */
  const touchY = useRef(0)

  // 内容变化 → 贴底。following 不进依赖：点「跟随」按钮的贴底由 stick() 命令式完成，
  // 免得「关 → 开」时多跑一次无意义的 effect。
  useEffect(() => {
    if (!onRef.current) return
    const el = ref.current
    if (!el) return
    // ⚠⚠ 别抢用户的滚动（2026-10-10 补，探针 T3 逮到的竞态）：原生 scroll 事件是
    //   **异步**派发的，从「用户把 scrollTop 挪走」到「事件到达处理器」之间，可以挤进
    //   一次内容增长。此时若无脑重贴底，用户的滚动会在事件开口前被抹掉 —— 随后事件
    //   只看得到 gap≈0，连 peel 的机会都没有（键盘 PageUp、滚动条微拖都会踩）。
    //   判定：位置明显比上次落点更靠上、且不在底部 → 是「用户在翻」，重贴让路。
    //   注意别用「不等就跳过」：内容变少时浏览器会把 scrollTop 夹回最大值（gap≈0），
    //   那种情况必须照常重贴，否则 stickyTop 会永远停在一个够不着的高度、跟随假死。
    if (el.scrollTop < stickyTop.current - 2 && el.scrollHeight - el.scrollTop - el.clientHeight >= 24) {
      return
    }
    const before = el.scrollTop
    el.scrollTop = el.scrollHeight
    // ⚠ 要在赋值之后读实际落点：赋 scrollHeight 会被浏览器夹到最大值。
    stickyTop.current = el.scrollTop
    // 位置真的动了才会有回波事件；没动就别占着令牌（旧令牌留给它自己那笔回波）。
    if (el.scrollTop !== before) echoPending.current = true
  }, [dep, ref])

  /** 贴上底、并保持跟随。「跟随」/「回到最新」按钮用它。 */
  const stick = useCallback(() => {
    const el = ref.current
    if (el) {
      const before = el.scrollTop
      el.scrollTop = el.scrollHeight
      stickyTop.current = el.scrollTop
      if (el.scrollTop !== before) echoPending.current = true
    }
    onRef.current = true
    setFollowing(true)
  }, [ref])

  /** 脱离跟随（用户明确要往回翻）。 */
  const peel = useCallback(() => {
    if (!onRef.current) return
    onRef.current = false
    setFollowing(false)
  }, [])

  const onScroll = useCallback(() => {
    const el = ref.current
    if (!el) return
    // ⚠ 先认领「我们自己指派的回波」：位置还停在上次落点、且该次指派尚未认领过事件。
    //   必须在所有分支**之前**判掉 —— 回波可能赶在用户 peel 之后才派发到，
    //   若让它流进下面的恢复分支，用户刚关掉的跟随会被静默复活（探针 T5 的实锤）。
    if (echoPending.current) {
      echoPending.current = false
      if (el.scrollTop === stickyTop.current) return
    }
    const gap = el.scrollHeight - el.scrollTop - el.clientHeight
    if (onRef.current) {
      if (gap < 24) return
      // scrollTop 一步没动、只是最大值长高了 —— 程序贴底的「回波」，不是用户滚的。
      if (el.scrollTop === stickyTop.current) return
      peel()
    } else if (gap < 8) {
      // 用户自己滚回了底部 → 恢复跟随，顺手贴死（免得差几个像素又算「不在底部」）
      stick()
    }
  }, [ref, peel, stick])

  const onWheel = useCallback(
    (e: WheelEvent<HTMLDivElement>) => {
      // 往上滚 = 想往回看。不等 scroll 事件那一下：要是被下一批日志先贴回去，
      // 用户会以为「滚不动 / 跟随关不掉」。
      if (e.deltaY < 0) peel()
    },
    [peel],
  )

  const onTouchStart = useCallback((e: TouchEvent<HTMLDivElement>) => {
    const t = e.touches[0]
    if (t) touchY.current = t.clientY
  }, [])

  const onTouchMove = useCallback(
    (e: TouchEvent<HTMLDivElement>) => {
      const t = e.touches[0]
      if (!t) return
      // 手指往下拖 = 内容被往回看
      if (t.clientY > touchY.current + 4) peel()
      touchY.current = t.clientY
    },
    [peel],
  )

  return {
    following,
    stick,
    /** 摊给滚动容器的四个事件 handler。 */
    scrollProps: { onScroll, onWheel, onTouchStart, onTouchMove },
  }
}
