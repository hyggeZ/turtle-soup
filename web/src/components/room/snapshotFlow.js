// 一个房间视图同时只读一份快照；请求期间的多次通知合成下一次读取。
export function createSingleFlight(run) {
  let active = null
  let queued = null

  function request() {
    if (active) {
      if (!queued) {
        let resolve
        let reject
        const promise = new Promise((done, fail) => { resolve = done; reject = fail })
        queued = { promise, resolve, reject }
      }
      return queued.promise
    }
    const current = Promise.resolve().then(run)
    active = current
    const finish = () => {
      active = null
      const next = queued
      queued = null
      if (next) request().then(next.resolve, next.reject)
    }
    current.then(finish, finish)
    return current
  }

  function cancelQueued() {
    queued?.resolve()
    queued = null
  }

  return { request, cancelQueued }
}

export function sameRoomView(current, expected) {
  return current.active && current.id === expected.id &&
    current.userId === expected.userId && current.epoch === expected.epoch
}
