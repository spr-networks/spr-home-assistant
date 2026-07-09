"""Constants for the SPR integration."""

from datetime import timedelta

DOMAIN = "spr"

DEFAULT_PORT = 8321
DEFAULT_SCAN_INTERVAL = timedelta(seconds=10)

CONF_CONSIDER_HOME = "consider_home"
DEFAULT_CONSIDER_HOME = 180  # seconds a device stays "home" after last seen

CONF_TRACK_NEW_DEVICES = "track_new_devices"
DEFAULT_TRACK_NEW_DEVICES = True

ATTR_MANUFACTURER = "Supernetworks"
ATTR_MODEL = "Secure Programmable Router"
