// Store manager dashboard API (Go service, /api/v1/stores/...).
import { get, send } from './client'

export type CrowdLevel = 'closed' | 'comfortable' | 'busy' | 'near_capacity'
/** live = running headcount (entries − exits), counted = hourly door counter, model = store forecast, capture-rate = formula on street density */
export type VisitorSource = 'live' | 'counted' | 'model' | 'capture-rate' | ''

export interface StoreInfo {
  id: string
  name: string
  sensor_id: string
  sensor_label: string
  lat: number
  lon: number
  config: {
    open_days: number[]
    open_from: number
    open_to: number
    capacity?: number
    visit_minutes?: number
    customers_per_cashier_h?: number
    min_staff?: number
    bakery_buy_rate?: number
    items_per_buyer?: number
    batch_size?: number
  }
}

/** Why an hour looks the way it does. Effects are relative changes of foot traffic (0.12 = +12%), forecast hours only. */
export interface HourWhy {
  weather?: string
  temp?: number
  precip?: number
  weather_effect?: number
  holiday_effect?: number
  school_effect?: number
  event?: string
  event_mult?: number
  /** where the event effect comes from: learned from earlier occurrences, past events of the same category, or the size default */
  event_basis?: 'profile' | 'learned' | 'category' | 'size'
  event_based_on?: string
  /** past hours: when the forecast behind the effects was made */
  effects_as_of?: string
  /** past hours: measured foot traffic vs the usual level for this weekday and hour (0.3 = 30% busier) */
  measured_vs_usual?: number
}

export type EventSize = 'S' | 'M' | 'L'

/** What the forecaster learned from a past event series (e.g. every year's Lichterfest). Multipliers: 1.5 = +50%. */
export interface LearnedEvent {
  series: string
  name: string
  category: string
  occurrences: number
  last_start: string
  next_name: string | null
  next_start: string | null
  street_peak: number
  street_mean: number
  store_mean: number | null
  at_store_street?: number
  at_store_shoppers?: number
  at_store_peak_hour?: number
}

export interface UpcomingEvent {
  id: number
  name: string
  category: string
  start: string
  end: string
  size: EventSize | null
  source: string
}

export interface NewEvent {
  name: string
  category: string
  start: string
  end: string
  sensor_ids: string[]
  size: EventSize
}

export interface DayContext {
  public_holiday?: string
  school_holiday?: string
  events: { name: string; category: string; start: string; end: string }[]
}

export interface DashHour {
  hour: number
  open: boolean
  entries: number
  occupancy: number
  occupancy_low?: number
  occupancy_high?: number
  source: VisitorSource
  level: CrowdLevel
  area_density: number
  area_source: 'actual' | 'forecast' | 'typical' | ''
  passers_by: number
  staff: number
  why?: HourWhy
  /** set when the store would normally be open but is closed, e.g. the public holiday's name */
  closed_reason?: string
}

export interface NowBlock {
  open: boolean
  occupancy: number
  pct: number
  level: CrowdLevel
  source: VisitorSource
  basis_from?: string
  basis_to?: string
  entries_last_hour?: number
  next_hour?: DashHour
  entries_counted_today: number
  live_at?: string
  /** closed: start of the next opening hour */
  next_open?: string
  entries_this_hour?: number
}

export interface SensorHealth {
  online: boolean
  minutes_silent: number
  battery: number
  risk: 'ok' | 'watch' | 'critical' | 'offline'
}

export interface AreaBlock {
  sensor_id: string
  label: string
  density: number | null
  typical: number
  vs_normal: number | null
  passers_by: number | null
  capture_today: number | null
  health: SensorHealth | null
}

export interface PeakBlock {
  hour: number
  occupancy: number
  pct: number
  level: CrowdLevel
  source: VisitorSource
  occupancy_low?: number
  occupancy_high?: number
}

export interface NearbyArea {
  sensor_id: string
  label: string
  distance_m: number
  density: number | null
  typical: number
  vs_normal: number | null
  online: boolean
  is_store_sensor: boolean
}

export interface DashAlert {
  severity: 'warn' | 'info' | 'ok'
  title: string
  detail: string
}

export interface StoreDashboard {
  store: StoreInfo
  capacity: number
  visit_minutes: number
  sim_now: string
  mode: string
  day: string
  weekday: string
  is_today: boolean
  now: NowBlock
  area: AreaBlock
  hours: DashHour[]
  peak: PeakBlock | null
  next_hours: DashHour[]
  nearby: NearbyArea[]
  week: { days: string[]; hours: number[]; occupancy: (number | null)[][]; source: string }
  alerts: DashAlert[]
  context: DayContext
  upcoming_events: UpcomingEvent[]
  learned_events: LearnedEvent[]
  forecast_run?: { id: number; as_of: string; model_version: string }
}

export interface StoreSummary {
  store: StoreInfo
  capacity: number
  now: NowBlock
  area: AreaBlock
  peak: PeakBlock | null
  hours: { hour: number; occupancy: number; source: VisitorSource }[]
  alerts: DashAlert[]
  context: DayContext
}

export interface StoresOverview {
  sim_now: string
  mode: string
  stores: StoreSummary[]
}

export const storesApi = {
  overview: (signal?: AbortSignal) => get<StoresOverview>('/v1/stores/overview', {}, signal),
  dashboard: (id: string, day: 'today' | 'tomorrow' | string, signal?: AbortSignal) =>
    get<StoreDashboard>(`/v1/stores/${encodeURIComponent(id)}/dashboard`, { day }, signal),
  plan: (id: string, day: string, signal?: AbortSignal) =>
    get<StorePlan>(`/v1/stores/${encodeURIComponent(id)}/plan`, { day }, signal),
  campaign: (id: string, signal?: AbortSignal) => get<Campaign>(`/v1/campaigns/${encodeURIComponent(id)}`, {}, signal),
  simulateCalm: (storeId: string, day: string) => send<CalmSimulation>('POST', '/v1/calm-hours/simulate', { store_id: storeId, day }),
  offers: (storeId: string, signal?: AbortSignal) => get<OfferStats>('/v1/calm-hours/offers', { store_id: storeId }, signal),
  sendOffer: (storeId: string, customerRef: string, usualDow: number) =>
    send<OfferResult>('POST', '/v1/calm-hours/offer', { store_id: storeId, customer_ref: customerRef, usual_dow: usualDow, usual_hour: -1 }),
  whatIf: (q: WhatIfRequest) => send<WhatIfResult>('POST', '/v1/whatif', q),
  addEvent: (e: NewEvent) => send<UpcomingEvent>('POST', '/v1/events', e),
  deleteEvent: (id: number) => send<{ ok: boolean }>('DELETE', `/v1/events/${id}`),
}

// ---------------------------------------------------------------- store plan (staffing + bakery)
export interface PlanHour {
  hour: number
  open: boolean
  density: number
  visitors: number
  cashiers: number
  fixed_cashiers: number
  bake_demand: number
  /** batches to put in the oven at this hour (ready for the next hour's demand) */
  batches: number
}

export interface PlanKpis {
  visitors_total: number
  peak_hour: number
  peak_visitors: number
  staff_hours_ebb: number
  staff_hours_fixed: number
  understaffed_hours_fixed: number
  overstaffed_hours_fixed: number
  bake_items_ebb: number
  bake_items_fixed: number
  bake_demand_total: number
  waste_items_ebb: number
  waste_items_fixed: number
  missed_sales_items_fixed: number
}

export interface StorePlan {
  store: StoreInfo
  day: string
  weekday: string
  hours: PlanHour[]
  kpis: PlanKpis
}

// ---------------------------------------------------------------- Calm Hours (app discounts for quieter hours)
export interface Campaign {
  store_id: string
  active: boolean
  config: { cap_pct_of_peak: number; acceptance: number; tiers: number[]; flex_hours: number; customers_per_week: number; weekly_budget_eur: number }
}

export interface CalmMode {
  load: number[]
  peak: number
  peak_hour: number
  overload_customer_hours: number
  staff_hours: number
  offers: number
  shifted_customers: number
  coupon_cost_eur: number
}

export interface CalmSimulation {
  store: string
  day: string
  weekday: string
  acceptance: number
  cap_pct: number
  result: { cap: number; open: boolean[]; none: CalmMode; naive: CalmMode; ebb: CalmMode; peak_reduction_pct: number; staff_hours_saved: number; loyalty_share: number }
}

export interface OfferStats {
  issued: number
  redeemed: number
  realised_acceptance?: number
}

export interface OfferResult {
  offer: null | { store: string; slot_start: string; slot_end: string; discount_pct: number; coupon_code: string; applies_to: string; reason: string }
  reason?: string
}

// ---------------------------------------------------------------- what-if
export type WeatherScenario = 'none' | 'sunny' | 'rain' | 'cold' | 'hot'

export interface WhatIfRequest {
  sensor_id: string
  scenario: WeatherScenario
  hours?: number
  event_size?: EventSize
  event_name?: string
  event_category?: string
  event_start?: string
  event_end?: string
}

export interface WhatIfPoint {
  ts: string
  baseline: number
  scenario: number
}

export interface WhatIfResult {
  as_of: string
  points: WhatIfPoint[]
  stores: { store_id: string; points: WhatIfPoint[] }[]
  event: null | { name: string; basis?: 'profile' | 'learned' | 'category' | 'size'; based_on?: string; mean_mult: number }
}
