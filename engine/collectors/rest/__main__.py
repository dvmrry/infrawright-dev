"""Executable entry point for the shared REST collector."""
import sys

from engine.collectors.rest import main


if __name__ == "__main__":
    sys.exit(main())
