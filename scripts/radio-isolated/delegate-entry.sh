#!/bin/sh
# Optional standalone container PID1 wrapper. Never used by the HA/S6 default.
set -eu
umask 077
refuse() { printf '%s\n' "radio delegation refused: $*" >&2; exit 125; }
[ "$#" -gt 0 ] || refuse 'original entrypoint required'
scope=${PODCONNECT_RADIO_DELEGATION_SCOPE:-}
case "$scope" in /pv-radio-*) ;; *) refuse 'explicit task scope required' ;; esac
case "${scope#/}" in ''|*[!a-zA-Z0-9_-]*) refuse 'invalid task scope' ;; esac
[ "$$" -eq 1 ] || refuse 'must be initial PID1'
# Builtins only until sole-process proof: a checker subprocess would count too.
count=0
for process in /proc/[0-9]*; do
  [ "$process" = /proc/1 ] || refuse 'other initial process visible'
  count=$((count + 1))
done
[ "$count" -eq 1 ] || refuse 'private proc namespace required'
[ -f /.dockerenv ] || refuse 'Docker container required'
uid_seen=0
seccomp_seen=0
caps_seen=0
while read -r key first second third fourth rest; do
  case "$key" in
    Uid:)
      [ "$first:$second:$third:$fourth" = 0:0:0:0 ] || refuse 'root manager required'
      uid_seen=1 ;;
    CapInh:|CapPrm:|CapEff:|CapBnd:|CapAmb:)
      case "$first" in ''|*[!a-fA-F0-9]*) refuse 'invalid kernel capability evidence' ;; esac
      # Only CHOWN, DAC_OVERRIDE, FOWNER, KILL, SETGID and SETUID. In
      # particular neither SYS_ADMIN nor NET_ADMIN may be present.
      [ "$((0x$first & ~0xeb))" -eq 0 ] || refuse 'excess container capabilities'
      caps_seen=$((caps_seen + 1)) ;;
    Seccomp:) [ "$first" = 2 ] || refuse 'seccomp filter required'; seccomp_seen=1 ;;
  esac
done < /proc/1/status
[ "$uid_seen:$seccomp_seen:$caps_seen" = 1:1:5 ] || refuse 'missing kernel process evidence'
IFS= read -r membership < /proc/1/cgroup
[ "$membership" = '0::/' ] || refuse 'private cgroup namespace root required'
# Private cgroup namespaces report the immediate host parent as '/..'. Tag the mount
# destination with the explicit scope; actual own-leaf inode checks below bind
# that parent to this namespace, never to a shared host cgroup root.
delegation="/run/task-cgroup$scope"
# Exactly one writable cgroup2 mount, one level above our namespace root.
# The ordinary namespace-root mount stays read-only. Never remount it.
awk -v destination="$delegation" '
  { fs=""; for(i=7;i<=NF;i++) if($i=="-") {fs=$(i+1);break} }
  fs=="cgroup2" {
    rw=("," $6 "," ~ /,rw,/)
    if(rw) { writable++; if($4!="/.." || $5!=destination) bad=1 }
    if($5=="/sys/fs/cgroup" && !rw) readonly++
  }
  END { exit !(writable==1 && readonly==1 && !bad) }
' /proc/1/mountinfo || refuse 'exact delegated mount required'
private_owner() {
  [ "$(stat -c '%u' "$1")" = 0 ] || refuse 'delegation not root-owned'
  mode=$(stat -c '%a' "$1")
  [ "$((0$mode & 0022))" -eq 0 ] || refuse 'delegation writable by another user'
}
private_owner "$delegation"
inode=$(stat -c '%i' /sys/fs/cgroup)
leaf=
for candidate in "$delegation"/*; do
  [ -d "$candidate" ] || continue
  [ ! -L "$candidate" ] || refuse 'symlink in delegated parent'
  if [ "$(stat -c '%i' "$candidate")" = "$inode" ]; then
    [ -z "$leaf" ] || refuse 'ambiguous current cgroup'
    leaf=$candidate
  fi
done
[ -n "$leaf" ] || refuse 'current cgroup outside delegation'
private_owner "$leaf"
[ "$(cat "$leaf/cgroup.type")" = domain ] || refuse 'domain cgroup required'
[ -z "$(cat "$leaf/cgroup.subtree_control")" ] || refuse 'controllers already enabled'
controllers=" $(cat "$leaf/cgroup.controllers") "
for controller in cpu memory pids; do
  case "$controllers" in *" $controller "*) ;; *) refuse 'required controller absent' ;; esac
done
count=0
while IFS= read -r pid; do
  [ "$pid" = 1 ] || refuse 'current leaf has another process'
  count=$((count + 1))
done < "$leaf/cgroup.procs"
[ "$count" -eq 1 ] || refuse 'current leaf membership unknown'
[ ! -e "$leaf/manager" ] || refuse 'manager child already exists'
# The only writes are inside our exact current leaf. No shared-parent writes.
mkdir "$leaf/manager"
printf '%s\n' 1 > "$leaf/manager/cgroup.procs"
while IFS= read -r pid; do refuse 'current leaf not empty after move'; done < "$leaf/cgroup.procs"
printf '%s' '+cpu +memory +pids' > "$leaf/cgroup.subtree_control"
# Diagnostic/test API only. Production authority remains options.json plus
# the manager's actual cgroup/clone3/kernel checks; this is no enable flag.
export PC_RADIO_CHAIN_CGROUP_PARENT="$leaf"
exec "$@"
