package models

//go:generate go-enum

// DataType is a user-facing toggle category of detection rules.
// CUSTOM is reserved for rules created via the configuration API that do not
// fit a built-in category.
//
// ENUM(
//
//	UNSPECIFIED=0
//	CREDENTIALS=1
//	API_KEYS=2
//	ACCESS_TOKENS=3
//	IP_ADDRESSES=4
//	PERSONAL_DATA=5
//	CUSTOM=6
//
// )
type DataType uint32
