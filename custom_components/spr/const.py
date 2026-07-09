"""Constants for the SPR integration."""

from datetime import timedelta

DOMAIN = "spr"

# HA reaches the plugin through SPR's authenticated API proxy. This is the
# proxy prefix; the plugin serves the /ha/v1/* routes behind it.
PROXY_BASE = "/plugins/home_assistant/ha/v1"

DEFAULT_VERIFY_SSL = False  # SPR ships a self-signed cert on the LAN

DEFAULT_SCAN_INTERVAL = timedelta(seconds=10)

CONF_CONSIDER_HOME = "consider_home"
DEFAULT_CONSIDER_HOME = 180  # seconds a device stays "home" after last seen

CONF_TRACK_NEW_DEVICES = "track_new_devices"
DEFAULT_TRACK_NEW_DEVICES = True

ATTR_MANUFACTURER = "Supernetworks"
ATTR_MODEL = "Secure Programmable Router"
