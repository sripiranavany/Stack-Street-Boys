package domain

// Defaults used when a store's config does not set capacity or visit length.
const (
	DefaultCapacity     = 150
	DefaultVisitMinutes = 15.0
)

// Crowd levels, by share of capacity.
const (
	LevelClosed       = "closed"
	LevelComfortable  = "comfortable"
	LevelBusy         = "busy"
	LevelNearCapacity = "near_capacity"
)

// CapacityOrDefault is the configured capacity, or DefaultCapacity.
func (c StoreConfig) CapacityOrDefault() int {
	if c.Capacity > 0 {
		return c.Capacity
	}
	return DefaultCapacity
}

// VisitMinutesOrDefault is the configured average visit length, or DefaultVisitMinutes.
func (c StoreConfig) VisitMinutesOrDefault() float64 {
	if c.VisitMinutes > 0 {
		return c.VisitMinutes
	}
	return DefaultVisitMinutes
}

// Occupancy estimates the average number of people inside during an hour from the entries counted at the door
// in that hour (Little's law: people inside = arrivals per minute x minutes each one stays).
func (c StoreConfig) Occupancy(entriesPerHour float64) float64 {
	if entriesPerHour <= 0 {
		return 0
	}
	return entriesPerHour * c.VisitMinutesOrDefault() / 60
}

// Level names how full the store is: under 70 % comfortable, 70-89 % busy, 90 % and more near capacity.
func Level(occupancy float64, capacity int, open bool) string {
	if !open {
		return LevelClosed
	}
	if capacity <= 0 {
		return LevelComfortable
	}
	p := occupancy / float64(capacity)
	switch {
	case p >= 0.9:
		return LevelNearCapacity
	case p >= 0.7:
		return LevelBusy
	default:
		return LevelComfortable
	}
}
