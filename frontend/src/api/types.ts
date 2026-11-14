export interface Sensor {
  sensor_id: string
  name: string
  latitude: number
  longitude: number
  first_seen: string | null
  last_seen: string | null
  is_test: boolean
  latest_value: number | null
  latest_time: string | null
  /** latest value / the sensor's own 95th percentile; 1 = "as busy as it gets" */
  busyness: number | null
}

export type Agg = '15m' | '1h' | '1d'

export interface TimeseriesPoint {
  time: string
  value: number | null
  normalized?: number
  n: number
}

export interface Timeseries {
  sensor_id: string
  name: string
  agg: Agg
  from: string
  to: string
  scale_p95: number | null
  points: TimeseriesPoint[]
}

export interface HeatPoint {
  sensor_id: string
  name: string
  latitude: number
  longitude: number
  value: number
  intensity: number
}

export interface HeatFrame {
  time: string
  points: HeatPoint[]
}

export interface HeatmapResponse {
  agg: 'hourly' | 'daily'
  from: string
  to: string
  frames: HeatFrame[]
}

export interface Patterns {
  sensor_id: string
  name: string
  days: string[]
  hours: number
  /** [weekday][hour] average value, null = no data */
  matrix: (number | null)[][]
  counts: number[][]
}

export interface Spike {
  sensor_id: string
  name: string
  latitude: number
  longitude: number
  time: string
  value: number
  expected: number
  z_score: number
  likely_reasons: string[]
}

export interface SpikesResponse {
  spikes: Spike[]
  missing_tables: string[]
}

export interface ForecastPoint {
  time: string
  value: number
  lower: number | null
  upper: number | null
}

export interface Forecast {
  sensor_id: string
  name: string
  based_on_data_until: string
  points: ForecastPoint[]
  note?: string
}

export interface WeatherRow {
  time: string
  temperature_c: number | null
  precipitation_mm: number | null
  wind_speed_ms: number | null
}

export interface Holiday {
  date: string
  name: string
  region: string | null
}

export interface CityEvent {
  name: string
  starts_at: string
  ends_at: string | null
  venue: string | null
}

export interface ContextData {
  weather: WeatherRow[]
  holidays: Holiday[]
  events: CityEvent[]
  missing_tables: string[]
}
