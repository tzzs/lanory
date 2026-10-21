# LuCI integration checks

These scripts run **on the router**, because the LuCI runtime (`luci.jsonc`,
`luci.template`, `nixio`) only exists there. They are the only way to verify the
integration without a browser.

```sh
# syntax-check the template: translates <% %> blocks into Lua and compiles them
lua tplcheck.lua /usr/lib/lua/luci/view/lanory/overview.htm

# exercise the data path the template uses: read the token, call the API,
# decode the JSON with luci.jsonc, print what the page will show
lua datapath.lua
```

Both were used to find two real defects that no unit test could catch:

- `require("luci.jsonc").parse(body)` is not valid Lua 5.1. Calling a method on
  the result of a call is a syntax error there, so the template failed to compile
  and the page would have shown a LuCI error page.
- `(require "luci.model.uci").cursor():get(...)` fails for the same reason.

`tplcheck.lua` reproduces LuCI's shorthand rules (`<%#` comment, `<%=` and `<%:`
translate) so a template is checked the way LuCI will actually read it.
