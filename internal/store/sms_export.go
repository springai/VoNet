package store

import (
	"context"
	"fmt"
	"sort"
)

// ExportSMSMessages visits every matching stored message without changing read
// state or contacting a modem. A single SELECT keeps one SQLite read snapshot;
// rows are streamed rather than accumulated in memory. The visitor must not
// query this Store, which may have only one database connection.
//
// Pagination fields are deliberately ignored. Conversations use the same
// hardware/subscription identity as ListSMSContacts, then chronological order
// with the durable row ID as a tie-breaker.
func (s *Store) ExportSMSMessages(ctx context.Context, filter SMSFilter, visit func(SMSMessage) error) error {
	filter.BeforeID = 0
	where, args := s.smsWhere(filter, "")
	rows, err := s.db.QueryContext(ctx, smsMessageSelect+where+`
		ORDER BY COALESCE(NULLIF(modem_imei, ''), 'device:' || device_id),
			COALESCE(NULLIF(iccid, ''), imsi, 'unknown'),
			message_time, id`, args...)
	if err != nil {
		return fmt.Errorf("query SMS export: %w", err)
	}
	defer rows.Close()
	var currentHardware, currentSubscription string
	var group []SMSMessage
	flush := func() error {
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Peer != group[j].Peer {
				return group[i].Peer < group[j].Peer
			}
			if !group[i].Timestamp.Equal(group[j].Timestamp) {
				return group[i].Timestamp.Before(group[j].Timestamp)
			}
			return group[i].ID < group[j].ID
		})
		for _, message := range group {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(message); err != nil {
				return fmt.Errorf("write SMS export: %w", err)
			}
		}
		group = group[:0]
		return nil
	}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		message, err := s.scanSMSMessage(rows)
		if err != nil {
			return fmt.Errorf("scan SMS export: %w", err)
		}
		hardware := smsHardwareKey(message.ModemIMEI, message.DeviceID)
		subscription := smsSubscriptionKey(message)
		if currentHardware != "" && (hardware != currentHardware || subscription != currentSubscription) {
			if err := flush(); err != nil {
				return err
			}
		}
		currentHardware, currentSubscription = hardware, subscription
		group = append(group, message)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate SMS export: %w", err)
	}
	if err := flush(); err != nil {
		return err
	}
	return ctx.Err()
}

func smsSubscriptionKey(message SMSMessage) string {
	if message.ICCID != "" {
		return message.ICCID
	}
	if message.IMSI != "" {
		return "imsi:" + message.IMSI
	}
	return "unknown"
}
