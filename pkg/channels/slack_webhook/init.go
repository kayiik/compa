package slackwebhook

import (
	"github.com/kayiik/compa/pkg/bus"
	"github.com/kayiik/compa/pkg/channels"
	"github.com/kayiik/compa/pkg/config"
)

func init() {
	channels.RegisterFactory(
		config.ChannelSlackWebHook,
		func(channelName, channelType string, cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
			bc := cfg.Channels[channelName]
			decoded, err := bc.GetDecoded()
			if err != nil {
				return nil, err
			}
			c, ok := decoded.(*config.SlackWebhookSettings)
			if !ok {
				return nil, channels.ErrSendFailed
			}
			ch, err := NewSlackWebhookChannel(bc, c, b)
			if err != nil {
				return nil, err
			}
			if channelName != config.ChannelSlackWebHook {
				ch.SetName(channelName)
			}
			return ch, nil
		},
	)
}
