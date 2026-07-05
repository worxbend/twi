package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/w0rxbend/twi/internal/config"
)

const setupUsage = `Usage:
  twi setup [--config path] [--non-interactive] [--username login] [--client-id id] [--channel name]...
            [--enable-kitty-images bool] [--enable-mouse bool]
            [--image-mode mode] [--avatar-mode mode] [--emoji-mode mode]
            [--emoji-provider provider] [--emote-mode mode] [--animation-mode mode]
            [--login | --login-dry-run]

Creates or updates the flat config file with non-secret settings: username,
client ID, default channels, image modes, emoji provider, mouse mode, and
animation mode.

Setup does not ask for or write OAuth tokens, refresh tokens, callback codes,
OAuth state, authorization URLs, or client secrets. Use the login handoff to
save tokens through the private credential store on supported Unix platforms.

Flags:
`

var setupInput io.Reader = os.Stdin

type setupCredentialAction string

const (
	setupCredentialSkip   setupCredentialAction = "skip"
	setupCredentialLogin  setupCredentialAction = "login"
	setupCredentialDryRun setupCredentialAction = "dry-run"
)

var (
	setupImageModes     = []string{"auto", "off", "small", "normal", "large"}
	setupAvatarModes    = []string{"off", "initials", "image"}
	setupEmojiModes     = []string{"unicode", "image"}
	setupEmojiProviders = []string{"twemoji", "custom"}
	setupEmoteModes     = []string{"text", "image"}
	setupAnimationModes = []string{"off", "reduced", "fast"}
)

type setupFlagOptions struct {
	cfgPath        string
	nonInteractive bool
	username       optionalTextFlag
	clientID       optionalTextFlag
	channels       channelFlags
	enableKitty    optionalBoolFlag
	enableMouse    optionalBoolFlag
	imageMode      enumFlag
	avatarMode     enumFlag
	emojiMode      enumFlag
	emojiProvider  enumFlag
	emoteMode      enumFlag
	animationMode  enumFlag
	login          bool
	loginDryRun    bool
}

func runSetup(args []string, stdout, stderr io.Writer) int {
	opts := setupFlagOptions{
		imageMode:     newEnumFlag("image mode", setupImageModes),
		avatarMode:    newEnumFlag("avatar mode", setupAvatarModes),
		emojiMode:     newEnumFlag("emoji mode", setupEmojiModes),
		emojiProvider: newEnumFlag("emoji provider", setupEmojiProviders),
		emoteMode:     newEnumFlag("emote mode", setupEmoteModes),
		animationMode: newEnumFlag("animation mode", setupAnimationModes),
	}
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.cfgPath, "config", "", "config file path")
	fs.BoolVar(&opts.nonInteractive, "non-interactive", false, "write config from flags and current defaults without prompts")
	fs.Var(&opts.username, "username", "Twitch login name to write to config")
	fs.Var(&opts.clientID, "client-id", "Twitch app client ID to write to config")
	fs.Var(&opts.channels, "channel", "default Twitch channel; repeat for multiple channels")
	fs.Var(&opts.enableKitty, "enable-kitty-images", "enable Kitty protocol image support")
	fs.Var(&opts.enableMouse, "enable-mouse", "enable terminal mouse support")
	fs.Var(&opts.imageMode, "image-mode", "image mode: auto, off, small, normal, or large")
	fs.Var(&opts.avatarMode, "avatar-mode", "avatar mode: off, initials, or image")
	fs.Var(&opts.emojiMode, "emoji-mode", "emoji mode: unicode or image")
	fs.Var(&opts.emojiProvider, "emoji-provider", "emoji image provider: twemoji or custom")
	fs.Var(&opts.emoteMode, "emote-mode", "emote mode: text or image")
	fs.Var(&opts.animationMode, "animation-mode", "animation mode: off, reduced, or fast")
	fs.BoolVar(&opts.login, "login", false, "run twi login after writing non-secret config")
	fs.BoolVar(&opts.loginDryRun, "login-dry-run", false, "run twi login --dry-run after writing non-secret config")
	fs.Usage = func() {
		fmt.Fprint(stderr, setupUsage)
		fs.PrintDefaults()
	}

	if hasHelpArg(args) {
		fmt.Fprint(stdout, setupUsage)
		fs.SetOutput(stdout)
		fs.PrintDefaults()
		return 0
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected setup argument %q\n\n", fs.Arg(0))
		fs.Usage()
		return 2
	}
	if opts.login && opts.loginDryRun {
		fmt.Fprintln(stderr, "choose only one of --login or --login-dry-run")
		return 2
	}

	cfg, err := config.Load(os.Environ(), config.Overrides{ConfigPath: opts.cfgPath})
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	applySetupFlagOptions(&cfg, opts)

	action := setupCredentialSkip
	if opts.login {
		action = setupCredentialLogin
	}
	if opts.loginDryRun {
		action = setupCredentialDryRun
	}
	if !opts.nonInteractive {
		wizard := setupWizard{
			reader: bufio.NewReader(setupInput),
			stdout: stdout,
		}
		action, err = wizard.Run(&cfg, action)
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(stderr, "setup canceled before all prompts were answered; rerun with --non-interactive for CI")
				return 2
			}
			fmt.Fprintf(stderr, "setup input: %v\n", err)
			return 1
		}
	}
	if err := validateAndNormalizeSetupConfig(&cfg); err != nil {
		fmt.Fprintf(stderr, "setup config: %s\n", config.RedactDisplayValue(err.Error()))
		return 2
	}

	if err := config.WriteNonSecretFile(cfg.Path, cfg); err != nil {
		fmt.Fprintf(stderr, "write config: %s\n", config.RedactDisplayValue(err.Error()))
		return 1
	}
	displayPath := config.RedactDisplayValue(cfg.Path)
	fmt.Fprintf(stdout, "Updated non-secret config: %s\n", displayPath)
	fmt.Fprintln(stdout, "Credential values stay outside config setup. On supported Unix platforms, use `twi login` to save tokens privately.")

	switch action {
	case setupCredentialLogin:
		fmt.Fprintln(stdout, "Starting login handoff.")
		return runLogin([]string{"--config", cfg.Path}, stdout, stderr)
	case setupCredentialDryRun:
		fmt.Fprintln(stdout, "Starting login dry run.")
		return runLogin([]string{"--config", cfg.Path, "--dry-run"}, stdout, stderr)
	default:
		fmt.Fprintf(stdout, "Next: run `twi login --config %s` when your Twitch app client ID and client secret are ready.\n", displayPath)
		return 0
	}
}

func applySetupFlagOptions(cfg *config.Config, opts setupFlagOptions) {
	if opts.username.set {
		cfg.Twitch.Username = strings.TrimSpace(opts.username.value)
	}
	if opts.clientID.set {
		cfg.Twitch.ClientID = strings.TrimSpace(opts.clientID.value)
	}
	if len(opts.channels) > 0 {
		cfg.DefaultChannels = append([]string(nil), opts.channels...)
	}
	if opts.enableKitty.set {
		cfg.Features.EnableKittyImages = opts.enableKitty.value
	}
	if opts.enableMouse.set {
		cfg.Features.EnableMouse = opts.enableMouse.value
	}
	if opts.imageMode.set {
		cfg.Features.ImageMode = opts.imageMode.value
	}
	if opts.avatarMode.set {
		cfg.Features.AvatarMode = opts.avatarMode.value
	}
	if opts.emojiMode.set {
		cfg.Features.EmojiMode = opts.emojiMode.value
	}
	if opts.emojiProvider.set {
		cfg.Features.EmojiProvider = opts.emojiProvider.value
	}
	if opts.emoteMode.set {
		cfg.Features.EmoteMode = opts.emoteMode.value
	}
	if opts.animationMode.set {
		cfg.Features.AnimationMode = opts.animationMode.value
	}
}

func validateAndNormalizeSetupConfig(cfg *config.Config) error {
	var err error
	if cfg.Features.ImageMode, err = normalizeSetupEnum("image mode", cfg.Features.ImageMode, setupImageModes); err != nil {
		return err
	}
	if cfg.Features.AvatarMode, err = normalizeSetupEnum("avatar mode", cfg.Features.AvatarMode, setupAvatarModes); err != nil {
		return err
	}
	if cfg.Features.EmojiMode, err = normalizeSetupEnum("emoji mode", cfg.Features.EmojiMode, setupEmojiModes); err != nil {
		return err
	}
	if cfg.Features.EmojiProvider, err = normalizeSetupEnum("emoji provider", cfg.Features.EmojiProvider, setupEmojiProviders); err != nil {
		return err
	}
	if cfg.Features.EmoteMode, err = normalizeSetupEnum("emote mode", cfg.Features.EmoteMode, setupEmoteModes); err != nil {
		return err
	}
	if cfg.Features.AnimationMode, err = normalizeSetupEnum("animation mode", cfg.Features.AnimationMode, setupAnimationModes); err != nil {
		return err
	}
	return nil
}

func normalizeSetupEnum(name, value string, allowed []string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if stringIn(value, allowed) {
		return value, nil
	}
	return "", fmt.Errorf("%s must be one of: %s", name, strings.Join(allowed, ", "))
}

type setupWizard struct {
	reader *bufio.Reader
	stdout io.Writer
}

func (w setupWizard) Run(cfg *config.Config, defaultAction setupCredentialAction) (setupCredentialAction, error) {
	fmt.Fprintln(w.stdout, "twi setup")
	fmt.Fprintf(w.stdout, "Config file: %s\n", cfg.Path)
	fmt.Fprintln(w.stdout, "Setup writes non-secret settings only. It never asks for OAuth tokens or client secrets.")

	username, err := w.promptText("Twitch username", cfg.Twitch.Username)
	if err != nil {
		return "", err
	}
	cfg.Twitch.Username = username

	clientID, err := w.promptText("Twitch app client ID", cfg.Twitch.ClientID)
	if err != nil {
		return "", err
	}
	cfg.Twitch.ClientID = clientID

	channels, err := w.promptChannels("Default channels", cfg.DefaultChannels)
	if err != nil {
		return "", err
	}
	cfg.DefaultChannels = channels

	enableKitty, err := w.promptBool("Enable Kitty images", cfg.Features.EnableKittyImages)
	if err != nil {
		return "", err
	}
	cfg.Features.EnableKittyImages = enableKitty

	imageMode, err := w.promptEnum("Image mode", cfg.Features.ImageMode, setupImageModes)
	if err != nil {
		return "", err
	}
	cfg.Features.ImageMode = imageMode

	avatarMode, err := w.promptEnum("Avatar mode", cfg.Features.AvatarMode, setupAvatarModes)
	if err != nil {
		return "", err
	}
	cfg.Features.AvatarMode = avatarMode

	emojiMode, err := w.promptEnum("Emoji mode", cfg.Features.EmojiMode, setupEmojiModes)
	if err != nil {
		return "", err
	}
	cfg.Features.EmojiMode = emojiMode

	emojiProvider, err := w.promptEnum("Emoji provider", cfg.Features.EmojiProvider, setupEmojiProviders)
	if err != nil {
		return "", err
	}
	cfg.Features.EmojiProvider = emojiProvider

	emoteMode, err := w.promptEnum("Emote mode", cfg.Features.EmoteMode, setupEmoteModes)
	if err != nil {
		return "", err
	}
	cfg.Features.EmoteMode = emoteMode

	animationMode, err := w.promptEnum("Animation mode", cfg.Features.AnimationMode, setupAnimationModes)
	if err != nil {
		return "", err
	}
	cfg.Features.AnimationMode = animationMode

	enableMouse, err := w.promptBool("Enable mouse", cfg.Features.EnableMouse)
	if err != nil {
		return "", err
	}
	cfg.Features.EnableMouse = enableMouse

	action, err := w.promptCredentialAction(defaultAction)
	if err != nil {
		return "", err
	}
	return action, nil
}

func (w setupWizard) promptText(label, current string) (string, error) {
	fmt.Fprintf(w.stdout, "%s [%s]: ", label, current)
	value, err := w.readLine()
	if err != nil {
		return "", err
	}
	if value == "" {
		return strings.TrimSpace(current), nil
	}
	return strings.TrimSpace(value), nil
}

func (w setupWizard) promptChannels(label string, current []string) ([]string, error) {
	fmt.Fprintf(w.stdout, "%s [%s]: ", label, strings.Join(current, ","))
	value, err := w.readLine()
	if err != nil {
		return nil, err
	}
	if value == "" {
		return normalizeSetupChannels(current), nil
	}
	return normalizeSetupChannels(strings.Split(value, ",")), nil
}

func (w setupWizard) promptBool(label string, current bool) (bool, error) {
	defaultValue := "n"
	if current {
		defaultValue = "y"
	}
	for {
		fmt.Fprintf(w.stdout, "%s (y/n) [%s]: ", label, defaultValue)
		value, err := w.readLine()
		if err != nil {
			return false, err
		}
		if value == "" {
			return current, nil
		}
		switch strings.ToLower(value) {
		case "y", "yes", "true", "1", "on":
			return true, nil
		case "n", "no", "false", "0", "off":
			return false, nil
		default:
			fmt.Fprintln(w.stdout, "Choose y or n.")
		}
	}
}

func (w setupWizard) promptEnum(label, current string, allowed []string) (string, error) {
	current = strings.ToLower(strings.TrimSpace(current))
	currentAllowed := stringIn(current, allowed)
	for {
		if currentAllowed {
			fmt.Fprintf(w.stdout, "%s (%s) [%s]: ", label, strings.Join(allowed, "/"), current)
		} else {
			fmt.Fprintf(w.stdout, "%s (%s): ", label, strings.Join(allowed, "/"))
		}
		value, err := w.readLine()
		if err != nil {
			return "", err
		}
		if value == "" {
			if currentAllowed {
				return current, nil
			}
			fmt.Fprintf(w.stdout, "Choose one of: %s.\n", strings.Join(allowed, ", "))
			continue
		}
		value = strings.ToLower(strings.TrimSpace(value))
		if stringIn(value, allowed) {
			return value, nil
		}
		fmt.Fprintf(w.stdout, "Choose one of: %s.\n", strings.Join(allowed, ", "))
	}
}

func (w setupWizard) promptCredentialAction(current setupCredentialAction) (setupCredentialAction, error) {
	if current == "" {
		current = setupCredentialSkip
	}
	allowed := []string{string(setupCredentialSkip), string(setupCredentialLogin), string(setupCredentialDryRun)}
	for {
		fmt.Fprintf(w.stdout, "Credential setup (%s) [%s]: ", strings.Join(allowed, "/"), current)
		value, err := w.readLine()
		if err != nil {
			return "", err
		}
		if value == "" {
			return current, nil
		}
		value = strings.ToLower(strings.TrimSpace(value))
		if stringIn(value, allowed) {
			return setupCredentialAction(value), nil
		}
		fmt.Fprintf(w.stdout, "Choose one of: %s.\n", strings.Join(allowed, ", "))
	}
}

func (w setupWizard) readLine() (string, error) {
	line, err := w.reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && line != "" {
			return strings.TrimSpace(line), nil
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func normalizeSetupChannels(values []string) []string {
	channels := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.TrimPrefix(value, "#"))
		if value != "" {
			channels = append(channels, value)
		}
	}
	return channels
}

type optionalTextFlag struct {
	value string
	set   bool
}

func (f *optionalTextFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("value cannot be empty")
	}
	f.value = value
	f.set = true
	return nil
}

func (f *optionalTextFlag) String() string {
	return f.value
}

type optionalBoolFlag struct {
	value bool
	set   bool
}

func (f *optionalBoolFlag) Set(value string) error {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	f.value = parsed
	f.set = true
	return nil
}

func (f *optionalBoolFlag) String() string {
	if !f.set {
		return ""
	}
	return strconv.FormatBool(f.value)
}

func (f *optionalBoolFlag) IsBoolFlag() bool {
	return true
}

type enumFlag struct {
	name    string
	allowed []string
	value   string
	set     bool
}

func newEnumFlag(name string, allowed []string) enumFlag {
	return enumFlag{name: name, allowed: allowed}
}

func (f *enumFlag) Set(value string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	if !stringIn(value, f.allowed) {
		return fmt.Errorf("%s must be one of: %s", f.name, strings.Join(f.allowed, ", "))
	}
	f.value = value
	f.set = true
	return nil
}

func (f *enumFlag) String() string {
	return f.value
}

func stringIn(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
