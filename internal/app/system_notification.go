package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/worxbend/twi/internal/twitch"
)

const (
	terminalBell               = "\a"
	desktopNotificationTimeout = 3 * time.Second
)

var errDesktopNotificationUnsupported = errors.New("desktop notifications unsupported")

// SystemNotifier delivers focus-aware notifications for non-chat Twitch events.
type SystemNotifier interface {
	Notify(context.Context, SystemNotification) error
}

// SystemNotification is the app-level notification payload for a normalized
// Twitch system event.
type SystemNotification struct {
	Title   string
	Body    string
	Channel string
	EventID string
}

type defaultSystemNotifier struct {
	desktop desktopNotifier
	bell    terminalBellNotifier
}

func newDefaultSystemNotifier(w io.Writer) SystemNotifier {
	return defaultSystemNotifier{
		desktop: desktopNotifier{},
		bell:    terminalBellNotifier{w: w},
	}
}

func (n defaultSystemNotifier) Notify(ctx context.Context, notification SystemNotification) error {
	if err := n.desktop.Notify(ctx, notification); err == nil {
		return nil
	}
	return n.bell.Notify(ctx, notification)
}

type terminalBellNotifier struct {
	w io.Writer
}

func (n terminalBellNotifier) Notify(ctx context.Context, _ SystemNotification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n.w == nil {
		return nil
	}
	_, err := io.WriteString(n.w, terminalBell)
	return err
}

type desktopNotifier struct {
	goos       string
	timeout    time.Duration
	lookPath   func(string) (string, error)
	runCommand func(context.Context, string, ...string) error
}

func (n desktopNotifier) Notify(ctx context.Context, notification SystemNotification) error {
	title := sanitizeSystemNotificationText(notification.Title, 96)
	body := sanitizeSystemNotificationText(notification.Body, 320)
	if title == "" {
		title = "twi"
	}

	goos := n.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	name, args, ok := desktopNotificationCommand(goos, title, body)
	if !ok {
		return errDesktopNotificationUnsupported
	}

	lookPath := n.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	path, err := lookPath(name)
	if err != nil {
		return err
	}

	timeout := n.timeout
	if timeout <= 0 {
		timeout = desktopNotificationTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	runCommand := n.runCommand
	if runCommand == nil {
		runCommand = runDesktopNotificationCommand
	}
	return runCommand(ctx, path, args...)
}

func runDesktopNotificationCommand(ctx context.Context, path string, args ...string) error {
	return exec.CommandContext(ctx, path, args...).Run()
}

func desktopNotificationCommand(goos, title, body string) (string, []string, bool) {
	switch goos {
	case "darwin":
		return "osascript", []string{
			"-e", "on run argv",
			"-e", "display notification item 2 of argv with title item 1 of argv",
			"-e", "end run",
			title,
			body,
		}, true
	case "windows":
		return "powershell.exe", []string{
			"-NoProfile",
			"-NonInteractive",
			"-ExecutionPolicy", "Bypass",
			"-EncodedCommand", windowsToastPowerShellCommand(title, body),
		}, true
	case "linux", "freebsd", "netbsd", "openbsd":
		args := []string{"--app-name=twi", "--urgency=normal", "--expire-time=8000", title}
		if body != "" {
			args = append(args, body)
		}
		return "notify-send", args, true
	default:
		return "", nil, false
	}
}

func windowsToastPowerShellCommand(title, body string) string {
	script := fmt.Sprintf(`
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] > $null
$template = @"
<toast><visual><binding template="ToastGeneric"><text>%s</text><text>%s</text></binding></visual></toast>
"@
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml($template)
$notifier = [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier("twi")
$notifier.Show([Windows.UI.Notifications.ToastNotification]::new($xml))
`, escapeNotificationXML(title), escapeNotificationXML(body))
	encoded := utf16.Encode([]rune(script))
	bytes := make([]byte, 0, len(encoded)*2)
	for _, r := range encoded {
		bytes = append(bytes, byte(r), byte(r>>8))
	}
	return base64.StdEncoding.EncodeToString(bytes)
}

func escapeNotificationXML(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch r {
		case '&':
			builder.WriteString("&amp;")
		case '<':
			builder.WriteString("&lt;")
		case '>':
			builder.WriteString("&gt;")
		case '"':
			builder.WriteString("&quot;")
		case '\'':
			builder.WriteString("&apos;")
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func sanitizeSystemNotificationText(value string, limit int) string {
	value = redactCredentialText(value)
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func systemNotificationFromMessage(message twitch.ChatMessage) (SystemNotification, bool) {
	eventID := normalizedSystemEventID(message)
	if eventID == "" {
		return SystemNotification{}, false
	}

	channel := normalizeChannelName(message.Channel)
	title := systemEventLabel(eventID)
	if title == "" {
		title = "Twitch event"
	}
	if channel != "" {
		title = fmt.Sprintf("%s in #%s", title, channel)
	}

	return SystemNotification{
		Title:   title,
		Body:    strings.TrimSpace(message.Text),
		Channel: channel,
		EventID: eventID,
	}, true
}

func normalizedSystemEventID(message twitch.ChatMessage) string {
	if eventID := strings.ToLower(strings.TrimSpace(message.SystemEventID)); eventID != "" {
		return eventID
	}
	if message.Type == twitch.MessageTypeSystem {
		return "system"
	}
	return ""
}

func systemEventLabel(eventID string) string {
	switch strings.ToLower(strings.TrimSpace(eventID)) {
	case "raid":
		return "Raid"
	case "unraid":
		return "Raid ended"
	case "sub", "resub", "multimonthsub":
		return "Subscription"
	case "subgift", "anonsubgift", "submysterygift":
		return "Gift subscription"
	case "giftpaidupgrade", "anongiftpaidupgrade":
		return "Gift upgrade"
	case "rewardgift":
		return "Reward gift"
	case "bitsbadgetier":
		return "Bits badge"
	case "announcement":
		return "Announcement"
	case "charitydonation":
		return "Charity donation"
	case "ritual":
		return "Ritual"
	case "chat_cleared", "user_banned", "user_timed_out", "message_deleted":
		return "Moderation"
	case "system":
		return "System"
	default:
		return humanizeSystemEventID(eventID)
	}
}

func humanizeSystemEventID(eventID string) string {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return ""
	}
	parts := strings.FieldsFunc(eventID, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	if len(parts) == 0 {
		return eventID
	}
	for i, part := range parts {
		part = strings.ToLower(part)
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

func systemNotificationSummary(notification SystemNotification) string {
	summary := strings.TrimSpace(notification.Title)
	body := strings.TrimSpace(notification.Body)
	if body == "" {
		return summary
	}
	if summary == "" {
		return body
	}
	if strings.EqualFold(summary, body) {
		return summary
	}
	return summary + ": " + body
}
