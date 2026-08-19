package domain

import (
	"errors"
	"math"
	"slices"
	"sort"
)

type SeatGroup struct {
	Seats []Seat  `json:"seats"`
	Score float64 `json:"score"`
}

type SeatRanker struct{}

func (SeatRanker) Rank(
	seatMap SeatMap,
	liveSeats []LiveSeat,
	count int,
	preference SeatPreference,
) ([]SeatGroup, error) {
	if count < 1 {
		return nil, errors.New("seat count must be positive")
	}
	available := make(map[string]bool, len(liveSeats))
	for _, live := range liveSeats {
		available[live.Label] = live.Available
	}
	rows := make(map[string][]Seat)
	for _, seat := range seatMap.Seats {
		if available[seat.Label] {
			rows[seat.Row] = append(rows[seat.Row], seat)
		}
	}

	var groups []SeatGroup
	for _, row := range rows {
		sort.Slice(row, func(i, j int) bool { return row[i].Number < row[j].Number })
		if !preference.Together {
			for _, seat := range row {
				groups = append(groups, SeatGroup{Seats: []Seat{seat}, Score: scoreSeat(seat, preference)})
			}
			continue
		}
		for start := 0; start+count <= len(row); start++ {
			candidate := row[start : start+count]
			if !consecutive(candidate) {
				continue
			}
			groups = append(groups, SeatGroup{
				Seats: append([]Seat(nil), candidate...),
				Score: scoreGroup(candidate, preference),
			})
		}
	}

	if !preference.Together && count > 1 {
		sort.Slice(groups, func(i, j int) bool { return groups[i].Score > groups[j].Score })
		if len(groups) < count {
			return nil, errors.New("not enough available seats")
		}
		picked := make([]Seat, 0, count)
		score := 0.0
		for _, group := range groups[:count] {
			picked = append(picked, group.Seats[0])
			score += group.Score
		}
		return []SeatGroup{{Seats: picked, Score: score}}, nil
	}

	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Score == groups[j].Score {
			return groups[i].Seats[0].Label < groups[j].Seats[0].Label
		}
		return groups[i].Score > groups[j].Score
	})
	return groups, nil
}

func consecutive(seats []Seat) bool {
	for index := 1; index < len(seats); index++ {
		previous, current := seats[index-1], seats[index]
		if current.Number != previous.Number+1 || previous.RightAisle || current.LeftAisle {
			return false
		}
	}
	return true
}

func scoreGroup(seats []Seat, preference SeatPreference) float64 {
	score := 0.0
	for _, seat := range seats {
		score += scoreSeat(seat, preference)
	}
	return score / float64(len(seats))
}

func scoreSeat(seat Seat, preference SeatPreference) float64 {
	score := 100.0
	if explicitRank := slices.Index(preference.ExplicitSeats, seat.Label); explicitRank >= 0 {
		score += 10_000 - float64(explicitRank*100)
	}
	if rowRank := slices.Index(preference.PreferredRows, seat.Row); rowRank >= 0 {
		score += 2_000 - float64(rowRank*100)
	}
	for _, zone := range preference.PreferredZones {
		if zone.Contains(seat.X, seat.Y) {
			score += float64(zone.Weight) * 10
		}
	}
	if typeRank := slices.Index(preference.PreferredTypes, seat.Type); typeRank >= 0 {
		score += 500 - float64(typeRank*25)
	}
	score -= math.Hypot(seat.X-0.5, seat.Y-0.55) * 100
	if preference.AvoidEdges && (seat.X < 0.08 || seat.X > 0.92) {
		score -= 500
	}
	return score
}
