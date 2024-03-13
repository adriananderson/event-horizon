package config

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"dario.cat/mergo"
	"github.com/siriusa51/goresult"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

type C struct {
	path        string
	files       []string
	Settings    map[any]any
	oldSettings map[any]any
	callbacks   []func(*C)
	l           *logrus.Logger
	reloadLock  sync.Mutex
}

func NewC(l *logrus.Logger) *C {
	return &C{
		Settings: make(map[any]any),
		l:        l,
	}
}

// Load will find all yaml files within path and load them in lexical order
func (c *C) Load(path string) error {
	c.path = path
	c.files = make([]string, 0)

	err := c.resolve(path, true)
	if err != nil {
		return err
	}

	if len(c.files) == 0 {
		return fmt.Errorf("no config files found at %s", path)
	}

	sort.Strings(c.files)

	err = c.parse()
	if err != nil {
		return err
	}

	return nil
}

func (c *C) LoadString(raw string) error {
	if raw == "" {
		return errors.New("empty configuration")
	}
	return c.parseRaw([]byte(raw))
}

// RegisterReloadCallback stores a function to be called when a config reload is triggered. The functions registered
// here should decide if they need to make a change to the current process before making the change. HasChanged can be
// used to help decide if a change is necessary.
// These functions should return quickly or spawn their own go routine if they will take a while
func (c *C) RegisterReloadCallback(f func(*C)) {
	c.callbacks = append(c.callbacks, f)
}

// InitialLoad returns true if this is the first load of the config, and ReloadConfig has not been called yet.
func (c *C) InitialLoad() bool {
	return c.oldSettings == nil
}

// HasChanged checks if the underlying structure of the provided key has changed after a config reload. The value of
// k in both the old and new settings will be serialized, the result of the string comparison is returned.
// If k is an empty string the entire config is tested.
// It's important to note that this is very rudimentary and susceptible to configuration ordering issues indicating
// there is change when there actually wasn't any.
func (c *C) HasChanged(k string) bool {
	if c.oldSettings == nil {
		return false
	}

	var (
		nv any
		ov any
	)

	if k == "" {
		nv = c.Settings
		ov = c.oldSettings
		k = "all settings"
	} else {
		nv = c.get(k, c.Settings)
		ov = c.get(k, c.oldSettings)
	}

	newVals, err := yaml.Marshal(nv)
	if err != nil {
		c.l.WithField("config_path", k).WithError(err).Error("Error while marshaling new config")
	}

	oldVals, err := yaml.Marshal(ov)
	if err != nil {
		c.l.WithField("config_path", k).WithError(err).Error("Error while marshaling old config")
	}

	return string(newVals) != string(oldVals)
}

// CatchHUP will listen for the HUP signal in a go routine and reload all configs found in the
// original path provided to Load. The old settings are shallow copied for change detection after the reload.
func (c *C) CatchHUP(ctx context.Context) {
	if c.path == "" {
		return
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)

	go func() {
		for {
			select {
			case <-ctx.Done():
				signal.Stop(ch)
				close(ch)
				return
			case <-ch:
				c.l.Info("Caught HUP, reloading config")
				c.ReloadConfig()
			}
		}
	}()
}

func (c *C) ReloadConfig() {
	c.reloadLock.Lock()
	defer c.reloadLock.Unlock()

	c.oldSettings = make(map[any]any)
	for k, v := range c.Settings {
		c.oldSettings[k] = v
	}

	err := c.Load(c.path)
	if err != nil {
		c.l.WithField("config_path", c.path).WithError(err).Error("Error occurred while reloading config")
		return
	}

	for _, v := range c.callbacks {
		v(c)
	}
}

func (c *C) ReloadConfigString(raw string) error {
	c.reloadLock.Lock()
	defer c.reloadLock.Unlock()

	c.oldSettings = make(map[any]any)
	for k, v := range c.Settings {
		c.oldSettings[k] = v
	}

	err := c.LoadString(raw)
	if err != nil {
		return err
	}

	for _, v := range c.callbacks {
		v(c)
	}

	return nil
}

// GetString will get the string for k or return an error
func (c *C) GetString(k string) goresult.Result[string] {
	r := c.Get(k)
	if r.IsError() {
		return goresult.Error[string](r.Error())
	}

	return goresult.Ok(fmt.Sprintf("%v", r))
}

// GetStringSlice will get the slice of strings for k or return an error
func (c *C) GetStringSlice(k string) goresult.Result[[]string] {
	r := c.Get(k)
	if r.IsError() {
		return goresult.Error[[]string](r.Error())
	}

	rv, ok := r.Unwrap().([]any)
	if !ok {
		return goresult.Error[[]string](errors.New("failed to parse"))
	}

	v := make([]string, len(rv))
	for i := 0; i < len(v); i++ {
		v[i] = fmt.Sprintf("%v", rv[i])
	}

	return goresult.Ok(v)
}

// GetMap will get the map for k or return an error
func (c *C) GetMap(k string) goresult.Result[map[any]any] {
	r := c.Get(k)
	if r.IsError() {
		return goresult.Error[map[any]any](r.Error())
	}

	v, ok := r.Unwrap().(map[any]any)
	if !ok {
		return goresult.Error[map[any]any](errors.New("failed to parse"))
	}

	return goresult.Ok(v)
}

// GetInt will get the int for k or return an error
func (c *C) GetInt(k string) goresult.Result[int] {
	r := c.GetString(k)
	if r.IsError() {
		return goresult.Error[int](r.Error())
	}
	v, err := strconv.Atoi(r.Unwrap())
	if err != nil {
		return goresult.Error[int](err)
	}

	return goresult.Ok(v)
}

// GetUint32 will get the uint32 for k or return an error
func (c *C) GetUint32(k string) goresult.Result[uint32] {
	r := c.GetInt(k)
	if r.IsError() {
		return goresult.Error[uint32](r.Error())
	}
	if uint64(r.Unwrap()) > uint64(math.MaxUint32) {
		return goresult.Error[uint32](errors.New("Number is too large"))
	}
	return goresult.Ok(uint32(r.Unwrap()))
}

// GetBool will get the bool for k or an error
func (c *C) GetBool(k string) goresult.Result[bool] {
	r := c.GetString(k)
	if r.IsError() {
		return goresult.Error[bool](r.Error())
	}
	v, err := strconv.ParseBool(strings.ToLower(r.Unwrap()))
	if err != nil {
		switch r.Unwrap() {
		case "y", "yes":
			return goresult.Ok(true)
		case "n", "no":
			return goresult.Ok(false)
		}
		return goresult.Error[bool](errors.New("failed to parse"))
	}

	return goresult.Ok(v)
}

// GetDuration will get the duration for k or return an error
func (c *C) GetDuration(k string) goresult.Result[time.Duration] {
	r := c.GetString(k)
	if r.IsError() {
		return goresult.Error[time.Duration](r.Error())
	}
	v, err := time.ParseDuration(r.Unwrap())
	if err != nil {
		return goresult.Error[time.Duration](err)
	}
	return goresult.Ok(v)
}

func (c *C) Get(k string) goresult.Result[any] {
	return c.get(k, c.Settings)
}

func (c *C) IsSet(k string) bool {
	v := c.get(k, c.Settings)
	return v.IsOk() && v.Unwrap() != nil
}

func (c *C) get(k string, v any) goresult.Result[any] {
	parts := strings.Split(k, ".")
	for _, p := range parts {
		m, ok := v.(map[any]any)
		if !ok {
			return goresult.Error[any](errors.New("failed to parse"))
		}

		v, ok = m[p]
		if !ok {
			return goresult.Error[any](errors.New("failed to parse"))
		}
	}

	return goresult.Ok(v)
}

// direct signifies if this is the config path directly specified by the user,
// versus a file/dir found by recursing into that path
func (c *C) resolve(path string, direct bool) error {
	i, err := os.Stat(path)
	if err != nil {
		return nil
	}

	if !i.IsDir() {
		c.addFile(path, direct)
		return nil
	}

	paths, err := readDirNames(path)
	if err != nil {
		return fmt.Errorf("problem while reading directory %s: %s", path, err)
	}

	for _, p := range paths {
		err := c.resolve(filepath.Join(path, p), false)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *C) addFile(path string, direct bool) error {
	ext := filepath.Ext(path)

	if !direct && ext != ".yaml" && ext != ".yml" {
		return nil
	}

	ap, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	c.files = append(c.files, ap)
	return nil
}

func (c *C) parseRaw(b []byte) error {
	var m map[any]any

	err := yaml.Unmarshal(b, &m)
	if err != nil {
		return err
	}

	c.Settings = m
	return nil
}

func (c *C) parse() error {
	var m map[any]any

	for _, path := range c.files {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		var nm map[any]any
		err = yaml.Unmarshal(b, &nm)
		if err != nil {
			return err
		}

		// We need to use WithAppendSlice so that firewall rules in separate
		// files are appended together
		err = mergo.Merge(&nm, m, mergo.WithAppendSlice)
		m = nm
		if err != nil {
			return err
		}
	}

	c.Settings = m
	return nil
}

func readDirNames(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	paths, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return nil, err
	}

	sort.Strings(paths)
	return paths, nil
}
