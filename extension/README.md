# desk-agent browser extension

Lets the agent show which **site** you are on while a browser is the focused app.

## What it sends, and what it never does

It sends the agent exactly two values, over the browser's native messaging channel
(a local pipe, never the network):

| Value | Meaning |
|---|---|
| `host` | Hostname of the active tab of the focused, non-private window, e.g. `github.com`. Empty when there is nothing to report. |
| `youtubeAudible` | `true` while a non-private YouTube tab is making sound. Only used to count videos. |

It never reads or sends a URL path, query, page title, page content, cookies, or history.
The `tabs` permission exists only so the extension can read a tab's address, and
`background.js` reduces it to a hostname before anything leaves the file. You can read the
whole thing: it is one 100-line file.

Defence in depth: `desk-agent native-host` drops any message with an unexpected field,
the agent drops anything that is not a plain hostname, and the agent turns the hostname
into a friendly name through `sites.toml`. Adult hosts are never named.

**Private windows.** Leave the browser's "Run in Private Windows" / "Allow in Incognito"
switch **off** (the default). The extension then cannot see private tabs at all, and it
reports nothing unless a normal window is the focused one. If you turn the switch on, it
still reports nothing for private windows.

## Install

1. Install the agent and register the native host (the installer does this for you):

   ```sh
   desk-agent install-native-host      # writes the manifest for each browser you have
   ```

2. Build the two loadable folders (the installer does this too):

   ```sh
   extension/build.sh                  # -> extension/dist/chromium, extension/dist/firefox
   ```

3. Load the extension.

   **Chrome / Chromium / Brave / Vivaldi / Edge:** open `chrome://extensions`, switch on
   *Developer mode*, *Load unpacked*, pick `extension/dist/chromium`. The extension id is
   pinned by the `key` in the manifest, so it is always
   `niohkiipopeggalllikokelcdfghlgic`, which is what the native-host manifest allows.

   **Firefox:** release Firefox only runs *signed* add-ons permanently. Pick one:
   - *Try it now (until restart):* `about:debugging#/runtime/this-firefox` →
     *Load Temporary Add-on* → `extension/dist/firefox/manifest.json`.
   - *Permanent:* sign it as a private ("unlisted") add-on with a free AMO account:
     `npx web-ext sign --channel=unlisted --source-dir extension/dist/firefox --api-key … --api-secret …`
     then open the resulting `.xpi` in Firefox.
   - *Alternative:* Firefox Developer Edition / Nightly / ESR, with
     `xpinstall.signatures.required = false` in `about:config`.

   Zen, LibreWolf and Floorp are Firefox forks and use the same files; whether they
   enforce add-on signing depends on the build.

4. Check it:

   ```sh
   desk-agent doctor     # "browser extension  connected: firefox" (or chromium)
   ```

## Uninstall

```sh
desk-agent uninstall-native-host
```

then remove the extension from the browser.

## Troubleshooting

- `doctor` says "no extension is connected": the extension is not loaded or not enabled, or the
  browser was started before `install-native-host` ran. Restart the browser once.
- Flatpak/Snap browsers sandbox native messaging and need extra permissions; this is not set
  up by the installer. (Untested.)
- Sites do not appear for a browser that is not focused: by design. The site is only reported
  while the browser is the focused app, and for a normal, focused window.
