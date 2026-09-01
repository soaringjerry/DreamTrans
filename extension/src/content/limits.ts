// Every request to Moodle goes through here: at most 3 in flight, at least
// 200 ms apart. No background polling exists anywhere in the extension.

export class RateLimiter {
  private active = 0
  private lastStart = 0
  private queue: Array<() => void> = []
  requests = 0
  cancelled = false

  constructor(private readonly concurrency = 3, private readonly minGapMs = 200) {}

  cancel(): void {
    this.cancelled = true
    for (const wake of this.queue.splice(0)) wake()
  }

  async run<T>(task: () => Promise<T>): Promise<T> {
    await this.acquire()
    if (this.cancelled) {
      this.release()
      throw new Error('cancelled')
    }
    const wait = Math.max(0, this.lastStart + this.minGapMs - Date.now())
    if (wait > 0) await new Promise((resolve) => setTimeout(resolve, wait))
    this.lastStart = Date.now()
    this.requests += 1
    try {
      return await task()
    } finally {
      this.release()
    }
  }

  private acquire(): Promise<void> {
    if (this.active < this.concurrency) {
      this.active += 1
      return Promise.resolve()
    }
    return new Promise((resolve) => {
      this.queue.push(() => {
        this.active += 1
        resolve()
      })
    })
  }

  private release(): void {
    this.active -= 1
    const next = this.queue.shift()
    if (next) next()
  }
}

/** Same-origin fetch with the tab's own cookies; never follows to other hosts. */
export async function moodleFetch(limiter: RateLimiter, url: string, init?: RequestInit): Promise<Response> {
  return limiter.run(async () => {
    const response = await fetch(url, { credentials: 'same-origin', redirect: 'follow', ...init })
    if (new URL(response.url).host !== new URL(url, location.href).host) {
      throw new Error(`redirected off Moodle: ${response.url}`)
    }
    if (response.status === 401 || response.status === 403) {
      throw new Error(`Moodle refused ${url} (${response.status})`)
    }
    return response
  })
}
