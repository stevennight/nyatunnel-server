package store

import "context"

// TrafficDelta is traffic metered since the last flush.
type TrafficDelta struct {
	TunnelID   string
	UserID     string
	BucketHour int64
	BytesIn    int64
	BytesOut   int64
	Conns      int64
}

// AddTraffic accumulates deltas into their hourly buckets.
func (s *Store) AddTraffic(ctx context.Context, deltas []TrafficDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, d := range deltas {
		if _, err := tx.ExecContext(ctx, `INSERT INTO traffic_stats (tunnel_id, user_id, bucket_hour, bytes_in, bytes_out, conns) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (tunnel_id, bucket_hour) DO UPDATE SET bytes_in = bytes_in + excluded.bytes_in,
			bytes_out = bytes_out + excluded.bytes_out, conns = conns + excluded.conns`,
			d.TunnelID, d.UserID, d.BucketHour, d.BytesIn, d.BytesOut, d.Conns); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TrafficTotal is the sum over a period.
type TrafficTotal struct {
	BytesIn  int64
	BytesOut int64
	Conns    int64
}

// Bytes is both directions together.
func (t TrafficTotal) Bytes() int64 { return t.BytesIn + t.BytesOut }

// TrafficByTunnel sums traffic per tunnel since the given hour.
func (s *Store) TrafficByTunnel(ctx context.Context, since int64) (map[string]TrafficTotal, error) {
	return s.trafficBy(ctx, "tunnel_id", since)
}

// TrafficByUser sums traffic per user since the given hour.
func (s *Store) TrafficByUser(ctx context.Context, since int64) (map[string]TrafficTotal, error) {
	return s.trafficBy(ctx, "user_id", since)
}

func (s *Store) trafficBy(ctx context.Context, col string, since int64) (map[string]TrafficTotal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+col+`, SUM(bytes_in), SUM(bytes_out), SUM(conns) FROM traffic_stats WHERE bucket_hour >= ? GROUP BY `+col, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]TrafficTotal{}
	for rows.Next() {
		var k string
		var t TrafficTotal
		if err := rows.Scan(&k, &t.BytesIn, &t.BytesOut, &t.Conns); err != nil {
			return nil, err
		}
		out[k] = t
	}
	return out, rows.Err()
}

// TrafficPoint is one hour of a series.
type TrafficPoint struct {
	Hour     int64 `json:"hour"`
	BytesIn  int64 `json:"bytesIn"`
	BytesOut int64 `json:"bytesOut"`
	Conns    int64 `json:"conns"`
}

// TrafficSeries returns hourly totals since the given hour, for one tunnel, one user, or everything
// (empty filters).
func (s *Store) TrafficSeries(ctx context.Context, tunnelID, userID string, since int64) ([]TrafficPoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT bucket_hour, SUM(bytes_in), SUM(bytes_out), SUM(conns) FROM traffic_stats
		WHERE bucket_hour >= ? AND (? = '' OR tunnel_id = ?) AND (? = '' OR user_id = ?) GROUP BY bucket_hour ORDER BY bucket_hour`,
		since, tunnelID, tunnelID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrafficPoint{}
	for rows.Next() {
		var p TrafficPoint
		if err := rows.Scan(&p.Hour, &p.BytesIn, &p.BytesOut, &p.Conns); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteTrafficBefore is retention housekeeping.
func (s *Store) DeleteTrafficBefore(ctx context.Context, before int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM traffic_stats WHERE bucket_hour < ?`, before)
	return err
}
