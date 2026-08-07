
- **2026-08-07 — reading the source instead of reasoning about it.** Wil sent
  R0rt1z2's XDA thread, which is canon for this device, and it settled three
  things I had been inferring.

  The reimage sequence in the recovery tool is **verbatim from the thread**,
  including the detail I had second-guessed: `adb push f1r30s.zip /sdcard/`
  comes *after* `twrp wipe data`, and the file is still there to be installed
  after the sideload. So the community's tested flow already depends on
  `/sdcard` surviving both, which is the question I could not answer and had
  hedged around. The hedge (re-check the patch after the flash) is now
  belt-and-braces rather than load-bearing, and cheap enough to keep.

  It also confirms, in the thread's own words, that "always flash the f1r30s
  ZIP after flashing a stock firmware image; otherwise, the OS will not boot."

  The part that changed the code is a sentence I had no idea about: **if
  neither slot contains a bootable OS and the device is booted repeatedly, the
  preloader's per-slot boot counters run out and the device bricks
  permanently.** The tool's failure message said recovery was "cheap from here
  and expensive once it has been power cycled" — which is a comfortable
  understatement of *this could kill the device*. It now says DO NOT REBOOT OR
  POWER CYCLE, in those words, and names the consequence. A message that
  leaves someone thinking a power cycle is worth a try is a bad message when
  the answer is a dead Dot.

  And the image check lost its escape hatch. I had built `--allow-unknown-image`
  on the reasoning that six FireOS 5 builds exist and pinning one would reject
  a valid image. Wil's answer was that EchoMuse targets the newest, its
  firmware is tested on it, and there is no reason to want an older — which
  makes the override strictly harmful: the only things it can admit are a
  corrupt download, an image for another device, or a FireOS 6 image, and the
  thread is explicit that only FireOS 5 boots once the exploit is installed.
  Flexibility that can only be used wrongly is not flexibility.

  A correction to the entry above, too. I had explained kylegordon's USB
  problem (#79) as stale `/data` surviving provisioning, since the wizard
  never wipes. The thread wipes userdata twice over — the GPT change does it,
  and `twrp wipe data` is step one of the install — so he should have arrived
  clean as well, and the mechanism mostly evaporates. Also worth noting for
  that issue: ADB on this device comes from f1r30s, which "enables ADB and
  UART console access, blocks OTA domains, and disables dm-verity" — so a
  device losing ADB after boot may be an f1r30s or firmware-build problem
  rather than a property problem at all. Forcing the properties is still
  harmless; we understand the cause less than the previous entry implies.

  Two guards on the new behaviour, both verified by reintroducing the bug.
  One of them first failed for the third time in this project the same way:
  the no-override check matched the word "allows" in a comment. Anchor on the
  call site, always — this time via the AST of the call rather than a
  substring of the file.
