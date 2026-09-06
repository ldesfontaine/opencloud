# Debian 12 avec systemd, pour jouer packaging/test-install.sh dans un
# conteneur jetable (make package-test). Rien de tout ça ne s'installe sur le
# poste de travail. Image de base épinglée par digest (politique Plumber).
FROM debian:12@sha256:6ebd97fa83deb272194a2cf015b3d26a4d538e9ad3a7a79d544c8af5b0a01443
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update -qq \
    && apt-get install -y -qq --no-install-recommends systemd systemd-sysv adduser procps ca-certificates curl \
       openssh-server openssh-client sudo \
    && rm -f /usr/sbin/policy-rc.d \
    && systemctl mask systemd-udevd.service systemd-udevd-kernel.socket systemd-udevd-control.socket \
       systemd-logind.service getty.target console-getty.service systemd-modules-load.service \
       systemd-timesyncd.service systemd-resolved.service
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
