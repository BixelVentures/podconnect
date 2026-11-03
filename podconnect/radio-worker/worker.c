#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/audit.h>
#include <linux/capability.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <sched.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/prctl.h>
#include <sys/resource.h>
#include <sys/syscall.h>
#include <sys/types.h>
#include <unistd.h>
#include <signal.h>
#include <string.h>

#ifndef RADIO_WORKER_TARGET
#define RADIO_WORKER_TARGET "/usr/sbin/owntone"
#endif
#define RADIO_WORKER_AS_BYTES (512UL*1024UL*1024UL)
#define RADIO_WORKER_TASKS 32
#define RADIO_WORKER_FILES 256

/* Fixed decoder launcher. The independent owner guardian supervises sampled
 * CPU/RSS and liveness; this helper does not claim a cgroup CPU quota. */
static void die(const char *s) { perror(s); _exit(125); }
static void limit(int resource, rlim_t value) {
  struct rlimit r = {value, value};
  if (setrlimit(resource, &r)) die("setrlimit");
}
#define DENY(n,e) BPF_JUMP(BPF_JMP|BPF_JEQ|BPF_K,(n),0,1), BPF_STMT(BPF_RET|BPF_K,SECCOMP_RET_ERRNO|(e))
static void lock_filter(void) {
#if defined(__aarch64__)
#define NATIVE_ARCH AUDIT_ARCH_AARCH64
#elif defined(__x86_64__)
#define NATIVE_ARCH AUDIT_ARCH_X86_64
#else
#error Unsupported proof architecture
#endif
  struct sock_filter f[] = {
    BPF_STMT(BPF_LD|BPF_W|BPF_ABS,offsetof(struct seccomp_data,arch)),
    BPF_JUMP(BPF_JMP|BPF_JEQ|BPF_K,NATIVE_ARCH,1,0),
    BPF_STMT(BPF_RET|BPF_K,SECCOMP_RET_KILL_PROCESS),
    BPF_STMT(BPF_LD|BPF_W|BPF_ABS,offsetof(struct seccomp_data,nr)),
#ifdef __x86_64__
    BPF_JUMP(BPF_JMP|BPF_JSET|BPF_K,0x40000000,0,1),
    BPF_STMT(BPF_RET|BPF_K,SECCOMP_RET_KILL_PROCESS),
#endif
#ifdef __NR_fork
    DENY(__NR_fork,EPERM),
#endif
#ifdef __NR_vfork
    DENY(__NR_vfork,EPERM),
#endif
#ifdef __NR_clone3
    DENY(__NR_clone3,ENOSYS),
#endif
    DENY(__NR_unshare,EPERM),
    DENY(__NR_setns,EPERM),
    DENY(__NR_ptrace,EPERM),
    DENY(__NR_process_vm_writev,EPERM),
    /* Only pthread-style same-thread-group clone is permitted. */
    BPF_JUMP(BPF_JMP|BPF_JEQ|BPF_K,__NR_clone,0,3),
    BPF_STMT(BPF_LD|BPF_W|BPF_ABS,offsetof(struct seccomp_data,args[0])),
    BPF_JUMP(BPF_JMP|BPF_JSET|BPF_K,CLONE_THREAD,1,0),
    BPF_STMT(BPF_RET|BPF_K,SECCOMP_RET_ERRNO|EPERM),
    BPF_STMT(BPF_RET|BPF_K,SECCOMP_RET_ALLOW)
  };
  struct sock_fprog p = {sizeof(f)/sizeof(f[0]), f};
  if (prctl(PR_SET_NO_NEW_PRIVS,1,0,0,0)) die("no_new_privs");
  if (prctl(PR_SET_SECCOMP,SECCOMP_MODE_FILTER,&p)) die("seccomp");
}
int main(int argc, char **argv) {
  uid_t uid=getuid(); gid_t gid=getgid();
  uid_t real_uid,effective_uid,saved_uid;
  gid_t real_gid,effective_gid,saved_gid;
  if (argc<2 || !uid || !gid || getresuid(&real_uid,&effective_uid,&saved_uid) || getresgid(&real_gid,&effective_gid,&saved_gid) || real_uid!=uid || effective_uid!=uid || saved_uid!=uid || real_gid!=gid || effective_gid!=gid || saved_gid!=gid) return 125;
  struct __user_cap_header_struct h = {_LINUX_CAPABILITY_VERSION_3,0};
  struct __user_cap_data_struct c[2] = {{0},{0}};
  if (syscall(SYS_capget,&h,c)) die("read capabilities");
  for (int i=0;i<2;i++) if(c[i].effective || c[i].permitted || c[i].inheritable) return 125;
  pid_t parent=getppid();
  if (parent<=1) return 125;
  limit(RLIMIT_AS,RADIO_WORKER_AS_BYTES);
  limit(RLIMIT_NPROC,RADIO_WORKER_TASKS);
  limit(RLIMIT_NOFILE,RADIO_WORKER_FILES);
  limit(RLIMIT_CORE,0);
  /* Bound glibc allocator arenas; it is a VAS cap, not an RSS claim. */
  if(setenv("MALLOC_ARENA_MAX","1",1)) die("allocator environment");
  /* Credential transitions reset PDEATHSIG; set it after the final transition. */
  if (prctl(PR_SET_PDEATHSIG,SIGKILL)) die("pdeathsig");
  if (getppid()!=parent) return 125;
  if(fcntl(5,F_SETFD,FD_CLOEXEC)) die("exec evidence pipe");
  lock_filter();
  char byte=1;
  if(write(3,&byte,1)!=1) return 125;
  close(3);
  if(read(4,&byte,1)!=1 || byte!=1 || getppid()!=parent) return 125;
  close(4);
  argv[0]=RADIO_WORKER_TARGET;
  execv(RADIO_WORKER_TARGET,argv);
  byte=1; (void)write(5,&byte,1);
  die("exec");
}
