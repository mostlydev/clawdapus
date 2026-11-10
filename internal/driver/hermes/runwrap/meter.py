"""Read-only task-home watchdog. It is a threshold check, never a hard quota."""
import errno
import os
import stat
import sys
import time


class BudgetExceeded(Exception):
    """The home holds more bytes than its threshold."""


class Unmeasured(Exception):
    """The home could not be measured within the scan caps."""


# Exit codes the supervisor distinguishes: 75 means too big; anything else,
# including 76, means the meter could not do its job.
EXIT_FULL, EXIT_UNMEASURED = 75, 76


# The scan caps exist to stop a pathological tree, not an ordinary checkout:
# a repository with its dependencies can hold hundreds of thousands of files.
def home_size(path, max_bytes, max_entries=5_000_000, max_seconds=60):
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC
    deadline = time.monotonic() + max_seconds
    total = entries = 0

    def walk(fd, depth):
        nonlocal total, entries
        if depth > 128:
            raise Unmeasured("depth")
        with os.scandir(fd) as children:
            for child in children:
                entries += 1
                if entries > max_entries or time.monotonic() > deadline:
                    raise Unmeasured("scan budget")
                try:
                    info = os.stat(child.name, dir_fd=fd, follow_symlinks=False)
                    if stat.S_ISREG(info.st_mode):
                        total += info.st_size
                        if total > max_bytes:
                            raise BudgetExceeded("size")
                    elif stat.S_ISDIR(info.st_mode):
                        # A concurrent rename/symlink swap cannot redirect the
                        # scan: every child is opened relative to a pinned fd.
                        sub = os.open(child.name, flags, dir_fd=fd)
                        try:
                            walk(sub, depth + 1)
                        finally:
                            os.close(sub)
                except OSError as exc:
                    if exc.errno not in (errno.ENOENT, errno.ELOOP, errno.ENOTDIR):
                        raise

    fd = os.open(path, flags)
    try:
        walk(fd, 0)
    finally:
        os.close(fd)
    return total


def main():
    path, maximum = sys.argv[1], int(sys.argv[2])
    if maximum <= 0:
        return 71
    while True:
        try:
            home_size(path, maximum)
        except BudgetExceeded:
            print("home_full", flush=True)
            return EXIT_FULL
        except (Unmeasured, OSError):
            # Too many entries to count in time, too deep, or unreadable. The
            # run still ends, but it is reported as a meter failure, so an
            # operator can tell "too big" from "could not be measured".
            print("home_unmeasured", flush=True)
            return EXIT_UNMEASURED
        time.sleep(1)


if __name__ == "__main__":
    sys.exit(main())
