// Package tdxprovider adapts github.com/injoyai/tdx to the platform's raw
// provider and normalization contracts. It deliberately exposes only the
// A-share datasets whose semantics are covered by this adapter version.
package tdxprovider

import "github.com/injoyai/strategy/internal/domain"

const (
	ProviderID      domain.ID = "tdx"
	ProviderVersion           = "v1-2b3dcae"
	ProviderName              = "TongdaXin Public Market Data"

	DatasetInstrument = "instrument"
	DatasetCalendar   = "calendar"
	DatasetBar        = "bar"

	MaxPageSize = 800
)

var instrumentFields = []domain.Field{
	{Name: "code", Type: domain.FieldString, Unit: "code", Nullable: false},
	{Name: "name", Type: domain.FieldString, Unit: "text", Nullable: false},
	{Name: "market", Type: domain.FieldString, Unit: "mic", Nullable: false},
	{Name: "asset_class", Type: domain.FieldString, Unit: "asset_class", Nullable: false},
	{Name: "currency", Type: domain.FieldString, Unit: "iso4217", Nullable: false},
	{Name: "listing_from", Type: domain.FieldTimestamp, Unit: "rfc3339", Nullable: true},
}

var calendarFields = []domain.Field{
	{Name: "session_start", Type: domain.FieldTimestamp, Unit: "rfc3339", Nullable: false},
	{Name: "session_end", Type: domain.FieldTimestamp, Unit: "rfc3339", Nullable: false},
	{Name: "is_half_day", Type: domain.FieldBoolean, Unit: "boolean", Nullable: false},
}

var barFields = []domain.Field{
	{Name: "open", Type: domain.FieldDecimal, Unit: "price", Nullable: false},
	{Name: "high", Type: domain.FieldDecimal, Unit: "price", Nullable: false},
	{Name: "low", Type: domain.FieldDecimal, Unit: "price", Nullable: false},
	{Name: "close", Type: domain.FieldDecimal, Unit: "price", Nullable: false},
	{Name: "volume", Type: domain.FieldDecimal, Unit: "shares", Nullable: false},
	{Name: "amount", Type: domain.FieldDecimal, Unit: "currency:CNY", Nullable: false},
	{Name: "unit", Type: domain.FieldString, Unit: "iso4217", Nullable: false},
}

func fieldsFor(dataset string) []domain.Field {
	switch dataset {
	case DatasetInstrument:
		return instrumentFields
	case DatasetCalendar:
		return calendarFields
	case DatasetBar:
		return barFields
	default:
		return nil
	}
}
