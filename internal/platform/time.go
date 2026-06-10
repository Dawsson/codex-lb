package platform

import (
	"database/sql"
	"strings"
	"time"
)

func SQLiteTimeToISO(value sql.NullString) *string {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999-07:00",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value.String); err == nil {
			iso := parsed.UTC().Format(time.RFC3339Nano)
			return &iso
		}
	}
	return &value.String
}

func UnixSecondsToISO(value sql.NullInt64) *string {
	if !value.Valid || value.Int64 <= 0 {
		return nil
	}
	iso := time.Unix(value.Int64, 0).UTC().Format(time.RFC3339)
	return &iso
}
