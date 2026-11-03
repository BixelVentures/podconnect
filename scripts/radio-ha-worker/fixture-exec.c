/* Test-only adapter. No kernel/custody overrides. Keeps exec in the SAME PID.
 * Production export/initialize use actual C; only finite provider + inert
 * receiver runtime is replaced with the existing real-C HTTP fixture. */
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

int main(int argc, char **argv) {
  if ((argc > 1 && (!strcmp(argv[1], "--radio-pairing-export") || !strcmp(argv[1], "--radio-pairing-export-known"))) || getenv("PODCONNECT_RADIO_INITIALIZE_ONLY")) {
    argv[0] = "/out/production-c-cli";
    execv(argv[0], argv);
    perror("exec production C"); return 125;
  }
  if (argc != 4 || strcmp(argv[1], "-f") || strcmp(argv[2], "-c") || !getenv("PODCONNECT_RADIO_SENDER")) return 125;
  FILE *f = fopen("/test-control/fixture.env", "r");
  if (!f) { perror("fixture env"); return 125; }
  char *line = NULL; size_t cap = 0;
  while (getline(&line, &cap, f) > 0) {
    line[strcspn(line, "\r\n")] = 0;
    char *eq = strchr(line, '=');
    if (!eq || strncmp(line, "PC_RADIO_HTTP_", 14)) return 125;
    *eq = 0; if (setenv(line, eq + 1, 1)) return 125;
  }
  free(line); fclose(f);
  if (setenv("PC_RADIO_HTTP_CONFIG", argv[3], 1)) return 125;
  char *args[] = {"/out/qualified-c-fixture", "--radio-http-owner", NULL};
  execv(args[0], args);
  perror("exec fixture C"); return 125;
}
