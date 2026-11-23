import { reactive, ref, watch } from 'vue'

// 发布和评论额度独立，按账号保存在当前标签页，离开表单后仍记得期限。
const waits = reactive({ soup: new Map(), comment: new Map() })

export function usePublishCooldown(kind, userId) {
  const deadlines = waits[kind]
  const seconds = ref(0)

  watch(() => deadlines.get(userId()), (until = 0, _previous, onCleanup) => {
    let timer
    function tick() {
      const left = Math.max(0, until - Date.now())
      seconds.value = Math.ceil(left / 1000)
      if (left > 0) timer = setTimeout(tick, Math.min(left, 1000))
    }
    onCleanup(() => clearTimeout(timer))
    tick()
  }, { immediate: true })

  function blocked(id) {
    return (deadlines.get(id) || 0) > Date.now()
  }

  function remember(id, error) {
    const now = Date.now()
    if (!id || error?.status !== 429 || !(error.retryAt > now)) return false
    // 迟到的响应仍归原账号。清理已过期记录，并保留并发响应给出的较晚期限。
    for (const [key, until] of deadlines) {
      if (until <= now) deadlines.delete(key)
    }
    deadlines.set(id, Math.max(deadlines.get(id) || 0, error.retryAt))
    return true
  }

  return { seconds, blocked, remember }
}
