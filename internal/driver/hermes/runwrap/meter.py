"""Read-only task-home watchdog. It is a threshold check, never a hard quota."""
import errno
import os
import stat
import sys
import time


class BudgetExceeded(Exception):
    pass


def home_size(path, max_bytes, max_entries=100000, max_seconds=2):
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC
    deadline = time.monotonic() + max_seconds
    total = entries = 0

    def walk(fd, depth):
        nonlocal total, entries
        if depth > 128:
            raise BudgetExceeded("depth")
        with os.scandir(fd) as children:
            for child in children:
                entries += 1
                if entries > max_entries or time.monotonic() > deadline:
                    raise BudgetExceeded("scan budget")
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
        except (BudgetExceeded, PermissionError):
            print("home_full", flush=True)
            return 75
        except OSError:
            print("home_unreadable", flush=True)
            return 75
        time.sleep(1)


if __name__ == "__main__":
    sys.exit(main())
