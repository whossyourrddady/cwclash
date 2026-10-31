export function subscriptionSummary(raw = '', now = Date.now()) {
  const values: Record<string, number> = {}
  for (const part of raw.split(';')) {
    const match = /^\s*(upload|download|total|expire)\s*=\s*(\d+)\s*$/i.exec(part)
    if (match && Number.isSafeInteger(Number(match[2]))) values[match[1].toLowerCase()] = Number(match[2])
  }
  const used = values.upload !== undefined && values.download !== undefined ? values.upload + values.download : null
  const total = values.total > 0 ? values.total : null
  const expiry = values.expire > 0 && values.expire * 1000 <= 8640000000000000 ? values.expire * 1000 : null
  return { used, total, remaining: total !== null && used !== null ? Math.max(0, total - used) : null,
    percent: total !== null && used !== null ? Math.min(100, used / total * 100) : null,
    expiry, expired: expiry !== null && expiry <= now }
}

export function orderNodes(names: string[], mode: string, delay: (name: string) => number | null) {
  return [...names].sort((a, b) => mode === 'name' ? a.localeCompare(b, 'zh-CN') : mode === 'delay'
    ? ((delay(a) || Infinity) - (delay(b) || Infinity)) : 0)
}

export type MeasuredDelay = { delay: number; baseline: string; url: string }
export const defaultTestURL = 'https://www.gstatic.com/generate_204'
export function historyKey(history: any[] | undefined): string { return JSON.stringify(history?.at(-1) ?? null) }
export function currentDelay(manual: MeasuredDelay | undefined, history: any[] | undefined, url: string): number | null {
  if (manual?.url === url && manual.baseline === historyKey(history)) return manual.delay
  // Core history doesn't identify its test URL; don't label it as a custom-URL result.
  if (url !== defaultTestURL) return null
  const value = history?.at(-1)?.delay
  return typeof value === 'number' ? value : null
}
