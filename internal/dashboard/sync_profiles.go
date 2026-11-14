package dashboard

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Laky-64/appleservices/internal/hmesync"
)

type syncProfile struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Token string `json:"token"`
}
type syncProfiles struct {
	Version  int           `json:"version"`
	Selected string        `json:"selected"`
	Profiles []syncProfile `json:"profiles"`
}
type syncProfileInput struct {
	Operation string `json:"operation"`
	ID        string `json:"id"`
	URL       string `json:"url"`
	Token     string `json:"token"`
}
type syncProfilePublic struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	KeySaved bool   `json:"key_saved"`
}

func (s *Server) readSyncProfiles() (syncProfiles, error) {
	value := syncProfiles{Version: 1, Profiles: []syncProfile{}}
	data, err := os.ReadFile(filepath.Join(s.config.Dir, "sync-profiles.dpapi"))
	if os.IsNotExist(err) {
		return value, nil
	}
	if err != nil || len(data) > 128<<10 {
		return value, errors.New("无法读取服务器配置")
	}
	plain, err := protectSyncProfiles(data, true)
	if err != nil {
		return value, err
	}
	defer clear(plain)
	if json.Unmarshal(plain, &value) != nil || value.Version != 1 || len(value.Profiles) > 50 {
		return syncProfiles{}, errors.New("服务器配置损坏，未覆盖原文件")
	}
	return value, nil
}
func (s *Server) writeSyncProfiles(value syncProfiles) error {
	plain, err := json.Marshal(value)
	if err != nil {
		return err
	}
	defer clear(plain)
	data, err := protectSyncProfiles(plain, false)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(s.config.Dir, 0700); err != nil {
		return errors.New("无法保存服务器配置")
	}
	file, err := os.CreateTemp(s.config.Dir, ".sync-profiles-*")
	if err != nil {
		return errors.New("无法保存服务器配置")
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), filepath.Join(s.config.Dir, "sync-profiles.dpapi"))
	}
	if err != nil {
		return errors.New("无法保存服务器配置")
	}
	return nil
}
func (s *Server) changeSyncProfiles(input syncProfileInput) (syncProfiles, error) {
	value, err := s.readSyncProfiles()
	if err != nil {
		return value, err
	}
	index := -1
	for i, p := range value.Profiles {
		if p.ID == input.ID {
			index = i
			break
		}
	}
	switch input.Operation {
	case "save":
		address := strings.TrimRight(strings.TrimSpace(input.URL), "/")
		if len(address) > 2048 {
			return value, errors.New("服务器地址过长")
		}
		if _, err = hmesync.Endpoint(address); err != nil {
			return value, errors.New("请填写有效的 HTTPS 服务器地址")
		}
		if input.ID != "" && index < 0 {
			return value, errors.New("服务器配置不存在，请重新选择")
		}
		token := input.Token
		if token == "" && index >= 0 && value.Profiles[index].URL == address {
			token = value.Profiles[index].Token
		}
		if len(token) < 32 || len(token) > 512 || strings.ContainsAny(token, " \t\r\n") {
			return value, errors.New("请填写至少 32 位的同步密钥；更换地址时需重新填写")
		}
		if index < 0 {
			if len(value.Profiles) >= 50 {
				return value, errors.New("最多保存 50 个服务器配置")
			}
			id := make([]byte, 16)
			if _, err = rand.Read(id); err != nil {
				return value, errors.New("无法创建服务器配置")
			}
			value.Profiles = append(value.Profiles, syncProfile{ID: hex.EncodeToString(id)})
			index = len(value.Profiles) - 1
		}
		value.Profiles[index].URL = address
		value.Profiles[index].Token = token
		value.Selected = value.Profiles[index].ID
	case "delete":
		if index < 0 {
			return value, errors.New("服务器配置不存在")
		}
		value.Profiles = append(value.Profiles[:index], value.Profiles[index+1:]...)
		if value.Selected == input.ID {
			value.Selected = ""
			if len(value.Profiles) > 0 {
				value.Selected = value.Profiles[0].ID
			}
		}
	case "select":
		if index < 0 {
			return value, errors.New("服务器配置不存在")
		}
		value.Selected = input.ID
	default:
		return value, errors.New("无效的配置操作")
	}
	return value, s.writeSyncProfiles(value)
}
func (s *Server) serveSyncProfiles(w http.ResponseWriter, r *http.Request) {
	s.syncProfilesMu.Lock()
	defer s.syncProfilesMu.Unlock()
	var value syncProfiles
	var err error
	if r.Method == "POST" {
		var input syncProfileInput
		if !readJSON(w, r, &input) {
			return
		}
		value, err = s.changeSyncProfiles(input)
	} else {
		value, err = s.readSyncProfiles()
	}
	if err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"message": err.Error()})
		return
	}
	public := make([]syncProfilePublic, 0, len(value.Profiles))
	for _, p := range value.Profiles {
		public = append(public, syncProfilePublic{p.ID, p.URL, p.Token != ""})
	}
	json.NewEncoder(w).Encode(struct {
		Selected string              `json:"selected"`
		Profiles []syncProfilePublic `json:"profiles"`
	}{value.Selected, public})
}
func (s *Server) resolveSyncProfile(id, address string) (syncProfile, error) {
	s.syncProfilesMu.Lock()
	defer s.syncProfilesMu.Unlock()
	value, err := s.readSyncProfiles()
	if err != nil {
		return syncProfile{}, err
	}
	for _, p := range value.Profiles {
		if p.ID == id {
			if p.URL != address {
				return syncProfile{}, errors.New("服务器地址已更改，请重新打开同步窗口确认")
			}
			return p, nil
		}
	}
	return syncProfile{}, errors.New("服务器配置已删除，请重新选择")
}
