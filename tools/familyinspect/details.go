package main

import (
	"errors"
	"fmt"
)

const legacyFamilyDetailsURL = "https://setup.icloud.com/setup/family/getFamilyDetails"

// Some FamilyKit responses contain only age information. Read the dedicated
// membership endpoint once; absence of membership is never interpreted as false.
func compatibleFamilyDetails(raw []byte, dsid string, fallback func() ([]byte, error)) (map[string]any, bool, error) {
	value, err := decodeObject(raw)
	if err != nil || dsid == "" || numericIDValue(value["dsid"]) != dsid || familyResponseError(value) {
		return nil, false, errors.New("家庭状态响应未通过账号核验")
	}
	if _, present := value["is-member-of-family"]; present {
		return value, false, validateFamilyDetails(value)
	}
	status, present := value["status"]
	if !present || fmt.Sprint(status) != "0" {
		return nil, false, errors.New("家庭状态响应未确认成功")
	}
	raw, err = fallback()
	if err != nil {
		return nil, false, err
	}
	value, err = decodeObject(raw)
	if err != nil || numericIDValue(value["dsid"]) != dsid {
		return nil, false, errors.New("家庭状态兼容响应账号不匹配")
	}
	if err = validateFamilyDetails(value); err != nil {
		return nil, false, err
	}
	return value, true, nil
}
