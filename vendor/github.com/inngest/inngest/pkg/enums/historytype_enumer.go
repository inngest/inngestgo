// Code generated by "enumer -trimprefix=HistoryType -type=HistoryType -json -text -gqlgen"; DO NOT EDIT.

package enums

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const _HistoryTypeName = "NoneFunctionScheduledFunctionStartedFunctionCompletedFunctionFailedFunctionCancelledFunctionStatusUpdatedStepScheduledStepStartedStepCompletedStepErroredStepFailedStepWaitingStepSleepingStepInvokingFunctionSkipped"

var _HistoryTypeIndex = [...]uint8{0, 4, 21, 36, 53, 67, 84, 105, 118, 129, 142, 153, 163, 174, 186, 198, 213}

const _HistoryTypeLowerName = "nonefunctionscheduledfunctionstartedfunctioncompletedfunctionfailedfunctioncancelledfunctionstatusupdatedstepscheduledstepstartedstepcompletedsteperroredstepfailedstepwaitingstepsleepingstepinvokingfunctionskipped"

func (i HistoryType) String() string {
	if i < 0 || i >= HistoryType(len(_HistoryTypeIndex)-1) {
		return fmt.Sprintf("HistoryType(%d)", i)
	}
	return _HistoryTypeName[_HistoryTypeIndex[i]:_HistoryTypeIndex[i+1]]
}

// An "invalid array index" compiler error signifies that the constant values have changed.
// Re-run the stringer command to generate them again.
func _HistoryTypeNoOp() {
	var x [1]struct{}
	_ = x[HistoryTypeNone-(0)]
	_ = x[HistoryTypeFunctionScheduled-(1)]
	_ = x[HistoryTypeFunctionStarted-(2)]
	_ = x[HistoryTypeFunctionCompleted-(3)]
	_ = x[HistoryTypeFunctionFailed-(4)]
	_ = x[HistoryTypeFunctionCancelled-(5)]
	_ = x[HistoryTypeFunctionStatusUpdated-(6)]
	_ = x[HistoryTypeStepScheduled-(7)]
	_ = x[HistoryTypeStepStarted-(8)]
	_ = x[HistoryTypeStepCompleted-(9)]
	_ = x[HistoryTypeStepErrored-(10)]
	_ = x[HistoryTypeStepFailed-(11)]
	_ = x[HistoryTypeStepWaiting-(12)]
	_ = x[HistoryTypeStepSleeping-(13)]
	_ = x[HistoryTypeStepInvoking-(14)]
	_ = x[HistoryTypeFunctionSkipped-(15)]
}

var _HistoryTypeValues = []HistoryType{HistoryTypeNone, HistoryTypeFunctionScheduled, HistoryTypeFunctionStarted, HistoryTypeFunctionCompleted, HistoryTypeFunctionFailed, HistoryTypeFunctionCancelled, HistoryTypeFunctionStatusUpdated, HistoryTypeStepScheduled, HistoryTypeStepStarted, HistoryTypeStepCompleted, HistoryTypeStepErrored, HistoryTypeStepFailed, HistoryTypeStepWaiting, HistoryTypeStepSleeping, HistoryTypeStepInvoking, HistoryTypeFunctionSkipped}

var _HistoryTypeNameToValueMap = map[string]HistoryType{
	_HistoryTypeName[0:4]:          HistoryTypeNone,
	_HistoryTypeLowerName[0:4]:     HistoryTypeNone,
	_HistoryTypeName[4:21]:         HistoryTypeFunctionScheduled,
	_HistoryTypeLowerName[4:21]:    HistoryTypeFunctionScheduled,
	_HistoryTypeName[21:36]:        HistoryTypeFunctionStarted,
	_HistoryTypeLowerName[21:36]:   HistoryTypeFunctionStarted,
	_HistoryTypeName[36:53]:        HistoryTypeFunctionCompleted,
	_HistoryTypeLowerName[36:53]:   HistoryTypeFunctionCompleted,
	_HistoryTypeName[53:67]:        HistoryTypeFunctionFailed,
	_HistoryTypeLowerName[53:67]:   HistoryTypeFunctionFailed,
	_HistoryTypeName[67:84]:        HistoryTypeFunctionCancelled,
	_HistoryTypeLowerName[67:84]:   HistoryTypeFunctionCancelled,
	_HistoryTypeName[84:105]:       HistoryTypeFunctionStatusUpdated,
	_HistoryTypeLowerName[84:105]:  HistoryTypeFunctionStatusUpdated,
	_HistoryTypeName[105:118]:      HistoryTypeStepScheduled,
	_HistoryTypeLowerName[105:118]: HistoryTypeStepScheduled,
	_HistoryTypeName[118:129]:      HistoryTypeStepStarted,
	_HistoryTypeLowerName[118:129]: HistoryTypeStepStarted,
	_HistoryTypeName[129:142]:      HistoryTypeStepCompleted,
	_HistoryTypeLowerName[129:142]: HistoryTypeStepCompleted,
	_HistoryTypeName[142:153]:      HistoryTypeStepErrored,
	_HistoryTypeLowerName[142:153]: HistoryTypeStepErrored,
	_HistoryTypeName[153:163]:      HistoryTypeStepFailed,
	_HistoryTypeLowerName[153:163]: HistoryTypeStepFailed,
	_HistoryTypeName[163:174]:      HistoryTypeStepWaiting,
	_HistoryTypeLowerName[163:174]: HistoryTypeStepWaiting,
	_HistoryTypeName[174:186]:      HistoryTypeStepSleeping,
	_HistoryTypeLowerName[174:186]: HistoryTypeStepSleeping,
	_HistoryTypeName[186:198]:      HistoryTypeStepInvoking,
	_HistoryTypeLowerName[186:198]: HistoryTypeStepInvoking,
	_HistoryTypeName[198:213]:      HistoryTypeFunctionSkipped,
	_HistoryTypeLowerName[198:213]: HistoryTypeFunctionSkipped,
}

var _HistoryTypeNames = []string{
	_HistoryTypeName[0:4],
	_HistoryTypeName[4:21],
	_HistoryTypeName[21:36],
	_HistoryTypeName[36:53],
	_HistoryTypeName[53:67],
	_HistoryTypeName[67:84],
	_HistoryTypeName[84:105],
	_HistoryTypeName[105:118],
	_HistoryTypeName[118:129],
	_HistoryTypeName[129:142],
	_HistoryTypeName[142:153],
	_HistoryTypeName[153:163],
	_HistoryTypeName[163:174],
	_HistoryTypeName[174:186],
	_HistoryTypeName[186:198],
	_HistoryTypeName[198:213],
}

// HistoryTypeString retrieves an enum value from the enum constants string name.
// Throws an error if the param is not part of the enum.
func HistoryTypeString(s string) (HistoryType, error) {
	if val, ok := _HistoryTypeNameToValueMap[s]; ok {
		return val, nil
	}

	if val, ok := _HistoryTypeNameToValueMap[strings.ToLower(s)]; ok {
		return val, nil
	}
	return 0, fmt.Errorf("%s does not belong to HistoryType values", s)
}

// HistoryTypeValues returns all values of the enum
func HistoryTypeValues() []HistoryType {
	return _HistoryTypeValues
}

// HistoryTypeStrings returns a slice of all String values of the enum
func HistoryTypeStrings() []string {
	strs := make([]string, len(_HistoryTypeNames))
	copy(strs, _HistoryTypeNames)
	return strs
}

// IsAHistoryType returns "true" if the value is listed in the enum definition. "false" otherwise
func (i HistoryType) IsAHistoryType() bool {
	for _, v := range _HistoryTypeValues {
		if i == v {
			return true
		}
	}
	return false
}

// MarshalJSON implements the json.Marshaler interface for HistoryType
func (i HistoryType) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.String())
}

// UnmarshalJSON implements the json.Unmarshaler interface for HistoryType
func (i *HistoryType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("HistoryType should be a string, got %s", data)
	}

	var err error
	*i, err = HistoryTypeString(s)
	return err
}

// MarshalText implements the encoding.TextMarshaler interface for HistoryType
func (i HistoryType) MarshalText() ([]byte, error) {
	return []byte(i.String()), nil
}

// UnmarshalText implements the encoding.TextUnmarshaler interface for HistoryType
func (i *HistoryType) UnmarshalText(text []byte) error {
	var err error
	*i, err = HistoryTypeString(string(text))
	return err
}

// MarshalGQL implements the graphql.Marshaler interface for HistoryType
func (i HistoryType) MarshalGQL(w io.Writer) {
	fmt.Fprint(w, strconv.Quote(i.String()))
}

// UnmarshalGQL implements the graphql.Unmarshaler interface for HistoryType
func (i *HistoryType) UnmarshalGQL(value interface{}) error {
	str, ok := value.(string)
	if !ok {
		return fmt.Errorf("HistoryType should be a string, got %T", value)
	}

	var err error
	*i, err = HistoryTypeString(str)
	return err
}
