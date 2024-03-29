// Code generated by "enumer -trimprefix=Period -type=Period -json -gqlgen -sql -text -transform=snake"; DO NOT EDIT.

package enums

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const _PeriodName = "noneminutehourdayweekmonth"

var _PeriodIndex = [...]uint8{0, 4, 10, 14, 17, 21, 26}

const _PeriodLowerName = "noneminutehourdayweekmonth"

func (i Period) String() string {
	if i < 0 || i >= Period(len(_PeriodIndex)-1) {
		return fmt.Sprintf("Period(%d)", i)
	}
	return _PeriodName[_PeriodIndex[i]:_PeriodIndex[i+1]]
}

// An "invalid array index" compiler error signifies that the constant values have changed.
// Re-run the stringer command to generate them again.
func _PeriodNoOp() {
	var x [1]struct{}
	_ = x[PeriodNone-(0)]
	_ = x[PeriodMinute-(1)]
	_ = x[PeriodHour-(2)]
	_ = x[PeriodDay-(3)]
	_ = x[PeriodWeek-(4)]
	_ = x[PeriodMonth-(5)]
}

var _PeriodValues = []Period{PeriodNone, PeriodMinute, PeriodHour, PeriodDay, PeriodWeek, PeriodMonth}

var _PeriodNameToValueMap = map[string]Period{
	_PeriodName[0:4]:        PeriodNone,
	_PeriodLowerName[0:4]:   PeriodNone,
	_PeriodName[4:10]:       PeriodMinute,
	_PeriodLowerName[4:10]:  PeriodMinute,
	_PeriodName[10:14]:      PeriodHour,
	_PeriodLowerName[10:14]: PeriodHour,
	_PeriodName[14:17]:      PeriodDay,
	_PeriodLowerName[14:17]: PeriodDay,
	_PeriodName[17:21]:      PeriodWeek,
	_PeriodLowerName[17:21]: PeriodWeek,
	_PeriodName[21:26]:      PeriodMonth,
	_PeriodLowerName[21:26]: PeriodMonth,
}

var _PeriodNames = []string{
	_PeriodName[0:4],
	_PeriodName[4:10],
	_PeriodName[10:14],
	_PeriodName[14:17],
	_PeriodName[17:21],
	_PeriodName[21:26],
}

// PeriodString retrieves an enum value from the enum constants string name.
// Throws an error if the param is not part of the enum.
func PeriodString(s string) (Period, error) {
	if val, ok := _PeriodNameToValueMap[s]; ok {
		return val, nil
	}

	if val, ok := _PeriodNameToValueMap[strings.ToLower(s)]; ok {
		return val, nil
	}
	return 0, fmt.Errorf("%s does not belong to Period values", s)
}

// PeriodValues returns all values of the enum
func PeriodValues() []Period {
	return _PeriodValues
}

// PeriodStrings returns a slice of all String values of the enum
func PeriodStrings() []string {
	strs := make([]string, len(_PeriodNames))
	copy(strs, _PeriodNames)
	return strs
}

// IsAPeriod returns "true" if the value is listed in the enum definition. "false" otherwise
func (i Period) IsAPeriod() bool {
	for _, v := range _PeriodValues {
		if i == v {
			return true
		}
	}
	return false
}

// MarshalJSON implements the json.Marshaler interface for Period
func (i Period) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.String())
}

// UnmarshalJSON implements the json.Unmarshaler interface for Period
func (i *Period) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("Period should be a string, got %s", data)
	}

	var err error
	*i, err = PeriodString(s)
	return err
}

// MarshalText implements the encoding.TextMarshaler interface for Period
func (i Period) MarshalText() ([]byte, error) {
	return []byte(i.String()), nil
}

// UnmarshalText implements the encoding.TextUnmarshaler interface for Period
func (i *Period) UnmarshalText(text []byte) error {
	var err error
	*i, err = PeriodString(string(text))
	return err
}

func (i Period) Value() (driver.Value, error) {
	return i.String(), nil
}

func (i *Period) Scan(value interface{}) error {
	if value == nil {
		return nil
	}

	var str string
	switch v := value.(type) {
	case []byte:
		str = string(v)
	case string:
		str = v
	case fmt.Stringer:
		str = v.String()
	default:
		return fmt.Errorf("invalid value of Period: %[1]T(%[1]v)", value)
	}

	val, err := PeriodString(str)
	if err != nil {
		return err
	}

	*i = val
	return nil
}

// MarshalGQL implements the graphql.Marshaler interface for Period
func (i Period) MarshalGQL(w io.Writer) {
	fmt.Fprint(w, strconv.Quote(i.String()))
}

// UnmarshalGQL implements the graphql.Unmarshaler interface for Period
func (i *Period) UnmarshalGQL(value interface{}) error {
	str, ok := value.(string)
	if !ok {
		return fmt.Errorf("Period should be a string, got %T", value)
	}

	var err error
	*i, err = PeriodString(str)
	return err
}
