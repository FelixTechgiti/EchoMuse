package buttons

import (
	"context"
	"errors"
	evdev "github.com/gvalkov/golang-evdev"
	"github.com/wilbowes/EchoMuse/pkg/buttons"
	"time"

	"github.com/wilbowes/EchoMuse/internal/androidsvc"
)

// Historical paths, now only the fallback when resolution by name fails.
// See resolve.go for why the numbering cannot be trusted.
const dotButton = "/dev/input/event1"
const volumeButton = "/dev/input/event2"

// VolumeCallback is called on volume button release with direction "up" or "down".
type VolumeCallback func(direction string)

// MuteCallback is called on mute button release.
type MuteCallback func()

type EvDevController struct {
	volumeCallback func(direction string)
	muteCallback   func()
}

// SetVolumeCallback registers a function to be called on volume button events.
// Must be called before SubscribeToButton.
func (e *EvDevController) SetVolumeCallback(cb func(direction string)) {
	e.volumeCallback = cb
}

// SetMuteCallback registers a function to be called on mute button events.
// Must be called before SubscribeToButton.
func (e *EvDevController) SetMuteCallback(cb func()) {
	e.muteCallback = cb
}

// Init the button listeners
// Kills alexa's native button functions
//
// THE ERROR IS FATAL TO THE WHOLE FIRMWARE — `NewButtonController` returns it
// and `cmd/server.go` calls log.Fatalf — which is why the stop goes through
// internal/androidsvc rather than exec'ing directly. Under emOS there is no
// `acebutton` and no property service for `stop` to talk to, and whether that
// is fatal rests on what Amazon's toolbox does when `property_set` has no
// socket: it happens to ignore the failure and exit 0, so the device boots.
// Resting the "does this Echo start at all" decision on a vendor binary's
// undocumented exit code is the thing being removed here, not a failure that
// was observed.
func (e *EvDevController) Init() error {
	_, err := androidsvc.Stop("acebutton")
	return err
}

func (e *EvDevController) SubscribeToButton(callback buttons.ButtonClickCallback) (*buttons.EventSubscription, error) {
	if callback == nil {
		return nil, errors.New("callback can't be nil")
	}

	dotBtn := e.GetDotButton()
	volBtn := e.GetVolumeButton()
	dotDevice, err := evdev.Open(resolveInputDevice(keypadDeviceName, dotButton))
	if err != nil {
		return nil, err
	}
	volDevice, err := evdev.Open(resolveInputDevice(volumeDeviceName, volumeButton))
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	eventSub := buttons.NewEventSubscription(cancel)

	readBtn := func(btn buttons.Button, btnDevice *evdev.InputDevice) {
		defer btnDevice.Release()

		beforeClickType := buttons.ClickType(0)
		beforeDown := false
		// When each click type was pressed, so a release can report how long
		// it was held. Keyed by click type because the dot device carries the
		// mute button too, and interleaving the two must not attribute one
		// button's hold to the other.
		downAt := map[buttons.ClickType]time.Time{}

		for {
			if ctx.Err() != nil {
				return
			}

			inputEvent, err := btnDevice.ReadOne()
			if err != nil {
				return
			}

			// Only key events. Every key press is followed immediately by
			// an EV_SYN separator whose Code and Value are both 0 — and
			// without this filter that SYN fell through to the Code==0
			// branch, took the previous click type, computed Value==1 as
			// FALSE, and fired a "release" microseconds after the press.
			//
			// So the button has always acted on the SYN rather than on the
			// real release, which is why it felt instant and why the actual
			// release (a genuine transition to 0) was then swallowed as a
			// no-change. Invisible until something needed to know how long
			// the button was held: heldMs came out at ~0 every time.
			if inputEvent.Type != evdev.EV_KEY {
				continue
			}

			clickType := buttons.ClickType(inputEvent.Code)
			if inputEvent.Code != 0 {
				beforeClickType = clickType
			} else {
				clickType = beforeClickType
			}

			down := inputEvent.Value == 1
			if beforeDown == down {
				continue
			}
			beforeDown = down

			// Intercept volume events on volume device
			if btn.Type == buttons.VolumeButton && !down {
				switch clickType {
				case buttons.VolumeUpClick:
					if e.volumeCallback != nil {
						e.volumeCallback("up")
					}
				case buttons.VolumeDownClick:
					if e.volumeCallback != nil {
						e.volumeCallback("down")
					}
				}
				continue
			}

			// Intercept mute on dot device
			if btn.Type == buttons.DotButton && !down && clickType == buttons.MuteClick {
				if e.muteCallback != nil {
					e.muteCallback()
				}
				continue
			}

			var heldMs int64
			if down {
				downAt[clickType] = time.Now()
			} else if t, ok := downAt[clickType]; ok {
				heldMs = time.Since(t).Milliseconds()
				delete(downAt, clickType)
			}

			callback(buttons.ButtonClickEvent{
				Button:    btn,
				ClickType: clickType,
				Down:      down,
				HeldMs:    heldMs,
			})
		}
	}

	go readBtn(dotBtn, dotDevice)
	go readBtn(volBtn, volDevice)

	return eventSub, nil
}

func (e *EvDevController) GetVolumeButton() buttons.Button {
	return buttons.Button{
		Type: buttons.VolumeButton,
	}
}

func (e *EvDevController) GetDotButton() buttons.Button {
	return buttons.Button{
		Type: buttons.DotButton,
	}
}

func NewButtonController() (*EvDevController, error) {
	controller := &EvDevController{}
	if err := controller.Init(); err != nil {
		return nil, err
	}
	return controller, nil
}
