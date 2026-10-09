//go:build paused_channels

package gateway

// The paused channels: only builds made with the paused_channels build tag
// include them. Matrix is in channel_matrix.go.
import (
	_ "github.com/kayiik/compa/pkg/channels/deltachat"
	_ "github.com/kayiik/compa/pkg/channels/dingtalk"
	_ "github.com/kayiik/compa/pkg/channels/discord"
	_ "github.com/kayiik/compa/pkg/channels/feishu"
	_ "github.com/kayiik/compa/pkg/channels/irc"
	_ "github.com/kayiik/compa/pkg/channels/line"
	_ "github.com/kayiik/compa/pkg/channels/maixcam"
	_ "github.com/kayiik/compa/pkg/channels/mqtt"
	_ "github.com/kayiik/compa/pkg/channels/onebot"
	_ "github.com/kayiik/compa/pkg/channels/qq"
	_ "github.com/kayiik/compa/pkg/channels/telegram"
	_ "github.com/kayiik/compa/pkg/channels/vk"
	_ "github.com/kayiik/compa/pkg/channels/wecom"
	_ "github.com/kayiik/compa/pkg/channels/weixin"
)
