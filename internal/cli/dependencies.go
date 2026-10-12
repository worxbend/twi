package cli

import (
	"context"
	"github.com/worxbend/twi/internal/app"
	"github.com/worxbend/twi/internal/auth"
	"github.com/worxbend/twi/internal/chatservice"
	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/debuglog"
	"github.com/worxbend/twi/internal/doctor"
	"github.com/worxbend/twi/internal/producer"
	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/twitch"
	"github.com/worxbend/twi/internal/twitch/helix"
	"io"
	"net/http"
	"os"
	"time"
)

// runner owns dependency overrides for one invocation. No mutable command factories are shared.
type runner struct {
	producerDelivery               func(context.Context, config.Config, credentialLoadStatus, producerOptions) (*producer.Service, error)
	recordReader                   func(context.Context, dataOptions, string) (*chatservice.Service, error)
	account                        string
	newNamedCredentialStore        func(string) (storage.CredentialStore, error)
	newLiveChatClient              func(ctx context.Context, cfg config.Config, holder *credentialHolder, logger debuglog.Logger, credentialStatus credentialLoadStatus, recorder app.ChatRecorder) (app.ChatSource, error)
	newLiveClientOptionsWithHolder func(cfg config.Config, holder *credentialHolder) app.ClientOptions
	runLiveChat                    func(io.Writer, config.Config, app.ChatSource, app.ClientOptions) error
	newDoctorTokenValidator        func() twitch.TokenValidator
	newLiveTokenValidator          func() twitch.TokenValidator
	doctorReachabilityProbe        func(context.Context) error
	doctorCacheDir                 func() string
	buildDoctorReport              func(ctx context.Context, cfg config.Config, cfgErr error) doctor.Report
	newCredentialStore             func() (storage.CredentialStore, error)
	newLoginFlow                   func() auth.LoginFlow
	newDeviceLoginFlow             func() auth.DeviceLoginFlow
	newLoginContext                func(timeout time.Duration) (context.Context, context.CancelFunc)
	newLoginCallbackWaiter         func(redirectURI string) (loginCallbackWaiter, error)
	openLoginBrowser               func(context.Context, string) error
	setupInput                     io.Reader
	browserExitGracePeriod         time.Duration
}

func newRunner() *runner {
	r := &runner{}
	r.producerDelivery = newProducerDelivery
	r.recordReader = func(ctx context.Context, o dataOptions, transport string) (*chatservice.Service, error) {
		return r.makeDataReader(ctx, o, transport)
	}
	r.newNamedCredentialStore = func(name string) (storage.CredentialStore, error) { return storage.NewNamedCredentialStore(name) }
	r.newLiveChatClient = func(ctx context.Context, cfg config.Config, holder *credentialHolder, logger debuglog.Logger, credentialStatus credentialLoadStatus, recorder app.ChatRecorder) (app.ChatSource, error) {
		return app.NewServiceIRCClient(ctx, liveIRCTransportFactory(cfg, holder, logger, credentialStatus), channelKeys(cfg.DefaultChannels), recorder)
	}
	r.newLiveClientOptionsWithHolder = func(cfg config.Config, holder *credentialHolder) app.ClientOptions {
		tokenSource := holder.tokenSource()
		return app.ClientOptions{
			StreamStatusResolver: newStreamStatusResolver(cfg, tokenSource),
			EmoteIndex:           newEmoteIndex(cfg, tokenSource),
			ChannelManager:       newChannelManager(cfg, tokenSource),
			GameLookup:           newGameLookup(cfg, tokenSource),
			UserLookup:           newUserLookup(cfg, tokenSource),
			MarkerManager:        newMarkerManager(cfg, tokenSource),
			FollowerLookup:       newFollowerLookup(cfg, tokenSource),
			SubscriptionLookup:   newSubscriptionLookup(cfg, tokenSource),
			ClipManager:          newClipManager(cfg, tokenSource),
			FollowedChannels:     newFollowedChannelLookup(cfg, tokenSource),
		}
	}
	r.runLiveChat = app.RunClientWithOptions
	r.newDoctorTokenValidator = func() twitch.TokenValidator {
		return helix.NewOAuthTokenValidator(helix.OAuthTokenValidatorConfig{
			HTTPClient: &http.Client{Timeout: 2 * time.Second},
		})
	}
	r.newLiveTokenValidator = func() twitch.TokenValidator {
		return r.newDoctorTokenValidator()
	}
	r.doctorReachabilityProbe = doctor.ProbeTwitchIRCReachability
	r.doctorCacheDir = func() string {
		return ""
	}
	r.buildDoctorReport = func(ctx context.Context, cfg config.Config, cfgErr error) doctor.Report {
		return doctor.RunWithOptions(ctx, cfg, doctor.Options{
			CacheDir:          r.doctorCacheDir(),
			ConfigLoadError:   cfgErr,
			ReachabilityProbe: r.doctorReachabilityProbe,
			TokenValidator:    r.newDoctorTokenValidator(),
		})
	}
	r.newCredentialStore = func() (storage.CredentialStore, error) {
		return storage.NewDefaultCredentialStore()
	}
	r.newLoginFlow = func() auth.LoginFlow {
		return auth.NewTwitchOAuthLoginFlow(auth.TwitchOAuthLoginFlowConfig{})
	}
	r.newDeviceLoginFlow = func() auth.DeviceLoginFlow {
		return auth.NewTwitchDeviceLogin(auth.DeviceLoginConfig{})
	}
	r.newLoginContext = func(timeout time.Duration) (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), timeout)
	}
	r.newLoginCallbackWaiter = func(redirectURI string) (loginCallbackWaiter, error) {
		return newLocalLoginCallbackWaiter(redirectURI)
	}
	r.openLoginBrowser = r.openBrowser
	r.setupInput = os.Stdin
	r.browserExitGracePeriod = 500 * time.Millisecond
	return r
}
func Run(args []string, stdout, stderr io.Writer) int { return newRunner().Run(args, stdout, stderr) }
func RunContext(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return newRunner().RunContext(ctx, args, stdin, stdout, stderr)
}
