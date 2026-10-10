import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type JavaRuntime } from './api'

/**
 * 本机可用的 Java 运行时。
 *
 * max 用来回答「这个构建在这台机器上跑不跑得起来」—— 选版本/构建时就得比，
 * 不能等下载完 50MB、点启动时才由体检拦下来：那时用户已经白下了一个用不了的包。
 *
 * max 为 null 表示还没问到（接口挂了 / 正在查）；0 表示确实一个可用 Java 都没有。
 * 两者必须分开：null 时闭嘴不提示（误报比不报更糟），0 时该如实报出来。
 *
 * maxRef 是同一个值的 ref。构建列表在加载时要读它来挑默认项，但加载函数
 * 不能因为它的变化而重建（会连带把列表重拉一遍），所以留个 ref 搭桥。
 *
 * list / reload 供 Java 选择器使用：装完一个新 JDK 之后要能重新扫一遍，
 * 让刚装好的那个立刻出现在列表里。
 */
export function useLocalJava() {
  const [list, setList] = useState<JavaRuntime[] | null>(null)
  const [max, setMax] = useState<number | null>(null)
  const maxRef = useRef<number | null>(null)
  const aliveRef = useRef(true)

  useEffect(() => {
    aliveRef.current = true
    return () => {
      aliveRef.current = false
    }
  }, [])

  const reload = useCallback(async () => {
    try {
      const d = await api.java()
      if (!aliveRef.current) return
      const items = d.items ?? []
      const majors = items.map((r) => r.major).filter((n) => n > 0)
      // 一个都没扫到就是 0（真的没有），不是 null（不知道）—— 这两个含义完全不同
      const top = majors.length > 0 ? Math.max(...majors) : 0
      maxRef.current = top
      setMax(top)
      setList(items)
    } catch {
      // 问不到就当不知道。报「你缺 Java 25」但其实只是接口没通，比不报更糟
    }
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  return { list, max, maxRef, reload }
}

/** top 为 null 表示未知（不判断）；0 表示本机一个可用 Java 都没有。 */
export function lacksJava(major: number, top: number | null) {
  return top !== null && major > 0 && major > top
}

/**
 * 缺 Java 时的「原因」短语（不含句号，留给调用方接着写后果）。
 * 三处提示共用一句，省得口径不一致。
 */
export function javaShortage(major: number, top: number | null) {
  if (top === null) return ''
  if (top === 0) return `本机一个可用的 Java 都没扫到，这个构建要 Java ${major}`
  return `本机最高只到 Java ${top}，这个构建要 Java ${major}`
}
