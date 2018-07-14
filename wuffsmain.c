#include <errno.h>
#include <unistd.h>

#define WUFFS_IMPLEMENTATION

#include "decode.c"

#ifndef DST_BUFFER_SIZE
#define DST_BUFFER_SIZE (16 * 1024)
#endif

#ifndef SRC_BUFFER_SIZE
#define SRC_BUFFER_SIZE (16 * 1024)
#endif

char dst_buffer[DST_BUFFER_SIZE];
char src_buffer[SRC_BUFFER_SIZE];

static void ignore_return_value(int ignored) {}

static const char* decode() {
	wuffs_zstd__decoder dec = ((wuffs_zstd__decoder){});
	wuffs_zstd__decoder__check_wuffs_version(&dec, sizeof dec, WUFFS_VERSION);

	while (true) {
		const int stdin_fd = 0;
		ssize_t n_src = read(stdin_fd, src_buffer, SRC_BUFFER_SIZE);
		if (n_src < 0) {
			if (errno != EINTR) {
				return strerror(errno);
			}
			continue;
		}

		wuffs_base__io_buffer src = ((wuffs_base__io_buffer){
				.ptr = src_buffer,
				.len = SRC_BUFFER_SIZE,
				.wi = n_src,
				.closed = n_src == 0,
		});
		wuffs_base__io_reader src_reader = wuffs_base__io_buffer__reader(&src);

		while (true) {
			wuffs_base__io_buffer dst = ((wuffs_base__io_buffer){.ptr = dst_buffer, .len = DST_BUFFER_SIZE});
			wuffs_base__io_writer dst_writer = wuffs_base__io_buffer__writer(&dst);
			wuffs_base__status s = wuffs_zstd__decoder__decode(&dec, dst_writer, src_reader);

			if (dst.wi) {
				const int stdout_fd = 1;
				ignore_return_value(write(stdout_fd, dst_buffer, dst.wi));
			}

			if (s == WUFFS_BASE__STATUS_OK) {
				return NULL;
			}
			if (s == WUFFS_BASE__SUSPENSION_SHORT_READ) {
				break;
			}
			if (s != WUFFS_BASE__SUSPENSION_SHORT_WRITE) {
				return wuffs_zstd__status__string(s);
			}
		}
	}
}

int fail(const char* msg) {
	const int stderr_fd = 2;
	ignore_return_value(write(stderr_fd, msg, strnlen(msg, 4095)));
	ignore_return_value(write(stderr_fd, "\n", 1));
	return 1;
}

int main(int argc, char** argv) {
	const char* msg = decode();
	int status = msg ? fail(msg) : 0;
	return status;
}
