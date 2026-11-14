import { onScopeDispose, ref, shallowRef, watch, type WatchSource } from 'vue'

/**
 * Reactive data loading: re-runs `fn` whenever `source` changes, aborts the
 * previous in-flight request, and exposes loading / error state.
 * `source` returning null/false skips the request.
 */
export function useAsyncData<T, S>(
  source: WatchSource<S | null | false>,
  fn: (value: S, signal: AbortSignal) => Promise<T>,
) {
  const data = shallowRef<T | null>(null)
  const error = ref<Error | null>(null)
  const loading = ref(false)
  let ctrl: AbortController | null = null

  async function run(value: S | null | false) {
    ctrl?.abort()
    if (value === null || value === false) {
      data.value = null
      loading.value = false
      return
    }
    const c = (ctrl = new AbortController())
    loading.value = true
    error.value = null
    try {
      const res = await fn(value, c.signal)
      if (!c.signal.aborted) data.value = res
    } catch (e) {
      if (!c.signal.aborted) {
        error.value = e as Error
        data.value = null
      }
    } finally {
      if (!c.signal.aborted) loading.value = false
    }
  }

  watch(source, run, { immediate: true, deep: true })
  onScopeDispose(() => ctrl?.abort())

  return { data, error, loading, reload: () => run((typeof source === 'function' ? source() : source.value) as S) }
}
