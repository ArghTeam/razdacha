# Dashboard design system

The embedded panel is a quiet operational console: neutral surfaces, readable names,
one blue accent and semantic observed states. System sans-serif and monospace stay
local; there are no external assets or build dependencies.

## Tokens and controls

Spacing follows 4, 8, 12, 16, 24 and 32 pixels. Buttons, icon controls, inputs and
navigation share a 40-pixel minimum height, rising to 44 pixels on narrow screens
and coarse pointers. Text wraps at enlarged sizes. Icon hit areas are square;
vertical-more glyphs are modest. Typography, radius, borders and status colors use
shared custom properties. Main content is bounded at 1240 pixels. Themes share geometry.

## Inventory

Every client, tunnel and rule has exactly one trailing vertical-more menu. Operations,
including enable/disable, checks and rule movement, live there. Body-mounted menus
retain keyboard/focus behavior and disabled movement boundaries. Names, observed state
and configured routes remain visible; configured routes never claim individual activation.

## Dialogs

Compact confirmations, regular editors and wide workspaces share header/body/footer
spacing, focus trapping, Escape/backdrop behavior and opener restoration. One body
scroller leaves header and footer visible. Small screens use nearly the full viewport;
confirmations stay compact. The rule workspace places resources beside routing and
client scope. Mobile reading order is name, resources, action, clients. Source search
and selected counts remain explicit. Catalog, client and selected-source pages display
up to six rows, retaining hidden selections in the complete saved draft. Pool groups
use the same bounded paging with search and one primary body scroller. Settings navigation stays visible during scrolling;
each section retains its own save boundary and status, without a global Save.

Client settings and WireGuard delivery share one dialog. Configuration, QR, filenames,
object URLs, drafts and pending responses retain existing modal/session lifetimes.
Routing needs Apply; enabled/deleted clients synchronize independently. Routing and
authentication semantics do not change.

## Acceptance

Synthetic browser fixtures cover menus, drafts, stale responses, secret cleanup, rule
integrity, keyboard handling and operation guards. Geometry checks cover matching
controls, one menu per row, wide rule composition, reachable actions, 320/390-pixel
screens, expanded catalogs, themes and enlarged text. Screenshots use synthetic data.
