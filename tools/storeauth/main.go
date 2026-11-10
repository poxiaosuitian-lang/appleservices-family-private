// storeauth exchanges credentials only over its parent's private pipes. It
// authenticates with the Store; it never submits a purchase or writes a keychain.
package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/majd/ipatool/v2/pkg/appstore"
	storehttp "github.com/majd/ipatool/v2/pkg/http"
	"github.com/majd/ipatool/v2/pkg/util/operatingsystem"
)

type loginClient interface {
	Login(appstore.LoginInput) (appstore.LoginOutput, error)
}

type loginFactory func(*memoryKeys, *trackingJar, fixedMachine) loginClient

func newLoginClient(keys *memoryKeys, jar *trackingJar, machine fixedMachine) loginClient {
	return appstore.NewAppStore(appstore.Args{
		Keychain: keys, CookieJar: jar, Machine: machine,
		OperatingSystem: operatingsystem.New(),
	})
}

func main() { os.Exit(runMain(os.Args[1:], os.Stdin, os.Stdout)) }

func runMain(args []string, input io.Reader, output io.Writer) (status int) {
	wire := newWire(input, output)
	defer func() {
		if recover() != nil {
			_ = wire.result(failure(stageInternal, nil))
			status = 1
		}
	}()
	// A helper has its own process, so this does not change its parent's proxy.
	// Also remove environment proxies used by the upstream root-certificate client.
	directTransport()
	if len(args) == 1 && args[0] == "--prepare" {
		if err := appstore.Prepare(context.Background()); err != nil {
			_ = wire.result(failure(stagePrepare, err))
			return 1
		}
		if wire.result(loginResult{OK: true}) != nil {
			return 1
		}
		return 0
	}
	if len(args) != 0 {
		_ = wire.result(failure(stageInput, nil))
		return 1
	}
	return runLogin(wire, newLoginClient)
}

func directTransport() {
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			_ = os.Unsetenv(key)
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	http.DefaultTransport = transport
}

func runLogin(wire *wire, factory loginFactory) int {
	finish := func(result loginResult) int {
		if wire.result(result) != nil || !result.OK {
			return 1
		}
		return 0
	}
	var request loginRequest
	if wire.read(&request) != nil {
		return finish(failure(stageProtocol, nil))
	}
	machine, stage := validateLogin(request)
	if stage != "" {
		return finish(failure(stage, nil))
	}
	keys := newMemoryKeys()
	defer keys.clear()
	if len(request.State) != 0 {
		if keys.Set(anisetteKey, request.State) != nil {
			return finish(failure(stageState, nil))
		}
	}
	jar, err := newTrackingJar()
	if err != nil {
		return finish(failure(stageInternal, nil))
	}
	client := factory(keys, jar, machine)
	login := appstore.LoginInput{Email: request.Email, Password: request.Password}
	result, err := client.Login(login)
	if jar.failure() != nil {
		return finish(failure(stageCookies, nil))
	}
	if errors.Is(err, appstore.ErrAuthCodeRequired) {
		if wire.challenge() != nil {
			return 1
		}
		var reply codeRequest
		if wire.read(&reply) != nil {
			return finish(failure(stageCodeRequired, nil))
		}
		code, ok := validateCode(reply)
		if !ok {
			return finish(failure(stageCodeInvalid, nil))
		}
		login.AuthCode = code
		result, err = client.Login(login)
		login.AuthCode, reply.Code = "", ""
		if errors.Is(err, appstore.ErrAuthCodeRequired) {
			return finish(failure(stageCodeInvalid, nil))
		}
	}
	login.Password, request.Password = "", ""
	if jar.failure() != nil {
		return finish(failure(stageCookies, nil))
	}
	if errors.Is(err, errMemoryState) {
		return finish(failure(stageState, nil))
	}
	if err != nil {
		return finish(failure(stageAuthentication, err))
	}
	response, stage := validatedResult(request, machine, result.Account)
	if stage != "" {
		return finish(failure(stage, nil))
	}
	response.Cookies = jar.snapshot()
	response.State, err = keys.Get(anisetteKey)
	if err != nil && !errors.Is(err, errKeyNotFound) {
		return finish(failure(stageState, nil))
	}
	if len(response.State) != 0 && !validState(response.State) {
		return finish(failure(stageState, nil))
	}
	return finish(response)
}

func failure(stage string, err error) loginResult {
	result := loginResult{Stage: stage}
	var upstream *storehttp.UnexpectedResponseError
	if errors.As(err, &upstream) && upstream.StatusCode >= 100 && upstream.StatusCode <= 599 {
		result.HTTPStatus = upstream.StatusCode
	}
	return result
}
