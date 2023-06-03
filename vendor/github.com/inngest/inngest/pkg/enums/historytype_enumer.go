// Code generated by "enumer -trimprefix=HistoryType -type=HistoryType -json -text"; DO NOT EDIT.

package enums

import (
	"encoding/json"
	"fmt"
	"strings"
)

const _HistoryTypeName = "NoneFunctionStartedFunctionCompletedFunctionFailedFunctionCancelledFunctionStatusUpdatedStepScheduledStepStartedStepCompletedStepErroredStepFailedStepWaitingStepSleeping"

var _HistoryTypeIndex = [...]uint8{0, 4, 19, 36, 50, 67, 88, 101, 112, 125, 136, 146, 157, 169}

const _HistoryTypeLowerName = "nonefunctionstartedfunctioncompletedfunctionfailedfunctioncancelledfunctionstatusupdatedstepscheduledstepstartedstepcompletedsteperroredstepfailedstepwaitingstepsleeping"

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
	_ = x[HistoryTypeFunctionStarted-(1)]
	_ = x[HistoryTypeFunctionCompleted-(2)]
	_ = x[HistoryTypeFunctionFailed-(3)]
	_ = x[HistoryTypeFunctionCancelled-(4)]
	_ = x[HistoryTypeFunctionStatusUpdated-(5)]
	_ = x[HistoryTypeStepScheduled-(6)]
	_ = x[HistoryTypeStepStarted-(7)]
	_ = x[HistoryTypeStepCompleted-(8)]
	_ = x[HistoryTypeStepErrored-(9)]
	_ = x[HistoryTypeStepFailed-(10)]
	_ = x[HistoryTypeStepWaiting-(11)]
	_ = x[HistoryTypeStepSleeping-(12)]
}

var _HistoryTypeValues = []HistoryType{HistoryTypeNone, HistoryTypeFunctionStarted, HistoryTypeFunctionCompleted, HistoryTypeFunctionFailed, HistoryTypeFunctionCancelled, HistoryTypeFunctionStatusUpdated, HistoryTypeStepScheduled, HistoryTypeStepStarted, HistoryTypeStepCompleted, HistoryTypeStepErrored, HistoryTypeStepFailed, HistoryTypeStepWaiting, HistoryTypeStepSleeping}

var _HistoryTypeNameToValueMap = map[string]HistoryType{
	_HistoryTypeName[0:4]:          HistoryTypeNone,
	_HistoryTypeLowerName[0:4]:     HistoryTypeNone,
	_HistoryTypeName[4:19]:         HistoryTypeFunctionStarted,
	_HistoryTypeLowerName[4:19]:    HistoryTypeFunctionStarted,
	_HistoryTypeName[19:36]:        HistoryTypeFunctionCompleted,
	_HistoryTypeLowerName[19:36]:   HistoryTypeFunctionCompleted,
	_HistoryTypeName[36:50]:        HistoryTypeFunctionFailed,
	_HistoryTypeLowerName[36:50]:   HistoryTypeFunctionFailed,
	_HistoryTypeName[50:67]:        HistoryTypeFunctionCancelled,
	_HistoryTypeLowerName[50:67]:   HistoryTypeFunctionCancelled,
	_HistoryTypeName[67:88]:        HistoryTypeFunctionStatusUpdated,
	_HistoryTypeLowerName[67:88]:   HistoryTypeFunctionStatusUpdated,
	_HistoryTypeName[88:101]:       HistoryTypeStepScheduled,
	_HistoryTypeLowerName[88:101]:  HistoryTypeStepScheduled,
	_HistoryTypeName[101:112]:      HistoryTypeStepStarted,
	_HistoryTypeLowerName[101:112]: HistoryTypeStepStarted,
	_HistoryTypeName[112:125]:      HistoryTypeStepCompleted,
	_HistoryTypeLowerName[112:125]: HistoryTypeStepCompleted,
	_HistoryTypeName[125:136]:      HistoryTypeStepErrored,
	_HistoryTypeLowerName[125:136]: HistoryTypeStepErrored,
	_HistoryTypeName[136:146]:      HistoryTypeStepFailed,
	_HistoryTypeLowerName[136:146]: HistoryTypeStepFailed,
	_HistoryTypeName[146:157]:      HistoryTypeStepWaiting,
	_HistoryTypeLowerName[146:157]: HistoryTypeStepWaiting,
	_HistoryTypeName[157:169]:      HistoryTypeStepSleeping,
	_HistoryTypeLowerName[157:169]: HistoryTypeStepSleeping,
}

var _HistoryTypeNames = []string{
	_HistoryTypeName[0:4],
	_HistoryTypeName[4:19],
	_HistoryTypeName[19:36],
	_HistoryTypeName[36:50],
	_HistoryTypeName[50:67],
	_HistoryTypeName[67:88],
	_HistoryTypeName[88:101],
	_HistoryTypeName[101:112],
	_HistoryTypeName[112:125],
	_HistoryTypeName[125:136],
	_HistoryTypeName[136:146],
	_HistoryTypeName[146:157],
	_HistoryTypeName[157:169],
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
