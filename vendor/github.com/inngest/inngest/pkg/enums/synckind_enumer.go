// Code generated by "enumer -trimprefix=SyncKind -type=SyncKind -json -gqlgen -sql -text -transform=snake"; DO NOT EDIT.

package enums

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const _SyncKindName = "nonein_bandout_of_band"

var _SyncKindIndex = [...]uint8{0, 4, 11, 22}

const _SyncKindLowerName = "nonein_bandout_of_band"

func (i SyncKind) String() string {
	if i < 0 || i >= SyncKind(len(_SyncKindIndex)-1) {
		return fmt.Sprintf("SyncKind(%d)", i)
	}
	return _SyncKindName[_SyncKindIndex[i]:_SyncKindIndex[i+1]]
}

// An "invalid array index" compiler error signifies that the constant values have changed.
// Re-run the stringer command to generate them again.
func _SyncKindNoOp() {
	var x [1]struct{}
	_ = x[SyncKindNone-(0)]
	_ = x[SyncKindInBand-(1)]
	_ = x[SyncKindOutOfBand-(2)]
}

var _SyncKindValues = []SyncKind{SyncKindNone, SyncKindInBand, SyncKindOutOfBand}

var _SyncKindNameToValueMap = map[string]SyncKind{
	_SyncKindName[0:4]:        SyncKindNone,
	_SyncKindLowerName[0:4]:   SyncKindNone,
	_SyncKindName[4:11]:       SyncKindInBand,
	_SyncKindLowerName[4:11]:  SyncKindInBand,
	_SyncKindName[11:22]:      SyncKindOutOfBand,
	_SyncKindLowerName[11:22]: SyncKindOutOfBand,
}

var _SyncKindNames = []string{
	_SyncKindName[0:4],
	_SyncKindName[4:11],
	_SyncKindName[11:22],
}

// SyncKindString retrieves an enum value from the enum constants string name.
// Throws an error if the param is not part of the enum.
func SyncKindString(s string) (SyncKind, error) {
	if val, ok := _SyncKindNameToValueMap[s]; ok {
		return val, nil
	}

	if val, ok := _SyncKindNameToValueMap[strings.ToLower(s)]; ok {
		return val, nil
	}
	return 0, fmt.Errorf("%s does not belong to SyncKind values", s)
}

// SyncKindValues returns all values of the enum
func SyncKindValues() []SyncKind {
	return _SyncKindValues
}

// SyncKindStrings returns a slice of all String values of the enum
func SyncKindStrings() []string {
	strs := make([]string, len(_SyncKindNames))
	copy(strs, _SyncKindNames)
	return strs
}

// IsASyncKind returns "true" if the value is listed in the enum definition. "false" otherwise
func (i SyncKind) IsASyncKind() bool {
	for _, v := range _SyncKindValues {
		if i == v {
			return true
		}
	}
	return false
}

// MarshalJSON implements the json.Marshaler interface for SyncKind
func (i SyncKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.String())
}

// UnmarshalJSON implements the json.Unmarshaler interface for SyncKind
func (i *SyncKind) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("SyncKind should be a string, got %s", data)
	}

	var err error
	*i, err = SyncKindString(s)
	return err
}

// MarshalText implements the encoding.TextMarshaler interface for SyncKind
func (i SyncKind) MarshalText() ([]byte, error) {
	return []byte(i.String()), nil
}

// UnmarshalText implements the encoding.TextUnmarshaler interface for SyncKind
func (i *SyncKind) UnmarshalText(text []byte) error {
	var err error
	*i, err = SyncKindString(string(text))
	return err
}

func (i SyncKind) Value() (driver.Value, error) {
	return i.String(), nil
}

func (i *SyncKind) Scan(value interface{}) error {
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
		return fmt.Errorf("invalid value of SyncKind: %[1]T(%[1]v)", value)
	}

	val, err := SyncKindString(str)
	if err != nil {
		return err
	}

	*i = val
	return nil
}

// MarshalGQL implements the graphql.Marshaler interface for SyncKind
func (i SyncKind) MarshalGQL(w io.Writer) {
	fmt.Fprint(w, strconv.Quote(i.String()))
}

// UnmarshalGQL implements the graphql.Unmarshaler interface for SyncKind
func (i *SyncKind) UnmarshalGQL(value interface{}) error {
	str, ok := value.(string)
	if !ok {
		return fmt.Errorf("SyncKind should be a string, got %T", value)
	}

	var err error
	*i, err = SyncKindString(str)
	return err
}