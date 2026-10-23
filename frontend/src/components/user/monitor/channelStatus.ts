import type { HealthState, MonitorHealth, MonitorMatrixBucket, MonitorMetric } from '@/api/channelMonitorV2'

/** Keep the backend's sample-aware health verdict; no traffic has no verdict. */
export function presentChannelStatus(
  overall: HealthState,
  metrics: Pick<MonitorMetric, 'request_count'>,
): HealthState {
  return metrics.request_count > 0 ? overall : 'unknown'
}

/**
 * Minimum requests before a window's ratios are treated as meaningful.
 *
 * Cache rate over one request is 0% or 100%, and a TTFT p50 over one request is
 * just that request's latency, so a rate needs some weight behind it.
 *
 * This floor deliberately matches the backend's own health scoring floor
 * (`minimum_sample`), which production runs at 5. It used to be 20 here while
 * the backend scored at 5, and the two disagreed in a way users saw: the
 * backend called a 6-request window healthy, the card refused to use that
 * window, walked back through history, and printed a cache rate from up to an
 * hour earlier as if it were current. A window the backend is willing to grade
 * is a window the card is allowed to show.
 */
export const CARD_MIN_SAMPLE = 5

/** Cache rate is a token ratio; require this many cache-scored tokens. */
export const CARD_MIN_CACHE_TOKENS = 200

export interface CardMetrics {
  /** Metric block the card renders. Never null: falls back to the row aggregate. */
  metrics: MonitorMetric
  health: MonitorHealth
  /** Newest bucket meeting the sample floor; null when only the aggregate qualified. */
  bucket: MonitorMatrixBucket | null
  /** True when no window reached the sample floor — render values as insufficient. */
  insufficient: boolean
  /** Requests behind the displayed metric (0 when redacted by the public API). */
  requestCount: number
  /** Tokens behind the displayed cache rate. */
  cacheSamples: number
  /**
   * True when the numbers come from the whole selected range rather than one
   * window. The card labels this, because a range aggregate and a 5-minute
   * window answer different questions and must not look identical.
   */
  fromRange: boolean
}

function ascending(buckets: MonitorMatrixBucket[]): MonitorMatrixBucket[] {
  return [...buckets]
    .filter(bucket => Boolean(bucket?.bucket_start) && Boolean(bucket?.metrics))
    .sort((a, b) => Date.parse(a.bucket_start) - Date.parse(b.bucket_start))
}

/**
 * Requests behind a metric block. `sample_count` survives the public API's
 * counter redaction specifically so a displayed rate can carry its weight;
 * `request_count` is only a fallback for admin payloads and older responses.
 */
function sampleSize(metrics: MonitorMetric): number {
  return metrics.sample_count ?? metrics.request_count ?? 0
}

function qualifies(metrics: MonitorMetric, minSample: number): boolean {
  return sampleSize(metrics) >= minSample
}

/**
 * Resolve the metric block a card should display.
 *
 * Preference order:
 *  1. the newest bucket that carries any traffic at all, so the card answers
 *     "what is happening now" rather than "what happened at the last busy
 *     minute". Walking backwards for a bigger sample is what made a 50-minute
 *     stale cache rate look current;
 *  2. the row-level range aggregate when there is no bucket in range at all,
 *     flagged `fromRange` so the card can say so;
 *  3. the row-level aggregate anyway, flagged insufficient.
 *
 * `minSample` only decides whether the chosen block is trustworthy enough to
 * headline; it no longer decides which window is shown.
 */
export function resolveCardMetrics(
  row: { metrics: MonitorMetric, health: MonitorHealth, buckets?: MonitorMatrixBucket[] },
  minSample: number = CARD_MIN_SAMPLE,
): CardMetrics {
  const ordered = ascending(row.buckets ?? [])

  // Newest window that actually carries traffic, regardless of how thin it is.
  let newestWithTraffic: MonitorMatrixBucket | null = null
  for (let index = ordered.length - 1; index >= 0; index -= 1) {
    if (sampleSize(ordered[index].metrics) > 0) {
      newestWithTraffic = ordered[index]
      break
    }
  }

  const chosen = newestWithTraffic
  const aggregateQualifies = qualifies(row.metrics, minSample)
  const metrics = chosen?.metrics ?? row.metrics
  const fromRange = chosen === null

  return {
    metrics,
    health: chosen?.health ?? row.health,
    bucket: chosen,
    // Insufficient only when the thing we actually show is too thin to trust.
    insufficient: !qualifies(metrics, minSample) && (chosen !== null || !aggregateQualifies),
    requestCount: sampleSize(metrics),
    cacheSamples: metrics.cache_rate_denominator ?? 0,
    fromRange,
  }
}

/** Cache rate is only trustworthy with enough cache-scored tokens behind it. */
export function cacheRateIsMeaningful(card: CardMetrics): boolean {
  if (card.insufficient) return false
  if (card.cacheSamples > 0) return card.cacheSamples >= CARD_MIN_CACHE_TOKENS
  // Redacted payloads drop the denominator; fall back to the request floor.
  return card.requestCount >= CARD_MIN_SAMPLE
}

/** TTFT needs a real percentile, not a single observation. */
export function ttftIsMeaningful(card: CardMetrics): boolean {
  if (card.insufficient) return false
  const samples = card.metrics.ttft?.sample_count ?? 0
  if (samples > 0) return samples >= CARD_MIN_SAMPLE
  return card.requestCount >= CARD_MIN_SAMPLE
}

/** Unit label for a trajectory slot width, in the viewer's language. */
export function bucketSpanUnit(seconds: number): 'day' | 'hour' | 'minute' {
  if (seconds > 0 && seconds % 86400 === 0) return 'day'
  if (seconds > 0 && seconds % 3600 === 0) return 'hour'
  return 'minute'
}

/** Raw slot width in its unit, so the caller can localise the label. */
export function bucketSpanValue(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '—'
  const unit = bucketSpanUnit(seconds)
  const divisor = unit === 'day' ? 86400 : unit === 'hour' ? 3600 : 60
  return String(seconds / divisor)
}
