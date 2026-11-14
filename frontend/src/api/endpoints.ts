import { get } from './client'
import type {
  Agg,
  ContextData,
  Forecast,
  HeatmapResponse,
  Patterns,
  Sensor,
  SpikesResponse,
  Timeseries,
} from './types'

export interface Range {
  from?: string
  to?: string
}

export const api = {
  sensors: (signal?: AbortSignal) => get<Sensor[]>('/sensors', {}, signal),

  timeseries: (id: string, r: Range & { agg?: Agg; normalize?: boolean }, signal?: AbortSignal) =>
    get<Timeseries>(`/sensors/${encodeURIComponent(id)}/timeseries`, { ...r }, signal),

  heatmap: (r: Range & { agg?: 'hourly' | 'daily' }, signal?: AbortSignal) =>
    get<HeatmapResponse>('/heatmap', { ...r }, signal),

  patterns: (id: string, r: Range = {}, signal?: AbortSignal) =>
    get<Patterns>(`/patterns/${encodeURIComponent(id)}`, { ...r }, signal),

  spikes: (r: Range & { sensor_id?: string; z?: number; limit?: number }, signal?: AbortSignal) =>
    get<SpikesResponse>('/events/spikes', { ...r }, signal),

  forecast: (id: string, horizon = '72h', signal?: AbortSignal) =>
    get<Forecast>(`/forecast/${encodeURIComponent(id)}`, { horizon }, signal),

  context: (r: Range, signal?: AbortSignal) => get<ContextData>('/context', { ...r }, signal),
}
