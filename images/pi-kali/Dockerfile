ARG KALI_BASE_IMAGE=kalilinux/kali-last-release@sha256:3ea545e38849417fc933514e117014b98aa42fd87d239177fa4c9874dcb73d5f
FROM ${KALI_BASE_IMAGE}

ARG DEBIAN_FRONTEND=noninteractive
ARG PI_VERSION=1.0.4
ARG KALI_MIRROR=http://http.kali.org/kali

RUN find /etc/apt -type f \( -name '*.list' -o -name '*.sources' \) \
        -exec sed -i \
            -e "s#http://http.kali.org/kali#${KALI_MIRROR}#g" \
            -e "s#https://http.kali.org/kali#${KALI_MIRROR}#g" \
            -e "s#http://kali.download/kali#${KALI_MIRROR}#g" \
            -e 's/kali-rolling/kali-last-snapshot/g' {} + \
    && grep -Rqs 'kali-last-snapshot' /etc/apt \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        kali-linux-core ca-certificates curl wget git jq ripgrep file procps \
        iproute2 iputils-ping dnsutils whois ftp netcat-openbsd ncat sshpass \
        proxychains4 socat openssh-client chisel nmap masscan tcpdump ffuf \
        gobuster dirsearch feroxbuster dirb wordlists nikto whatweb nuclei sqlmap \
        hydra binutils gdb strace ltrace gcc make python3 python3-pip python3-venv \
        nodejs npm unzip xxd php-cli traceroute iptables python3-requests \
        python3-bs4 python3-lxml python3-pil python3-pycryptodome python3-paramiko \
    && node -e 'const [M,m]=process.versions.node.split(".").map(Number); if (M < 22 || (M === 22 && m < 19)) { console.error("Pi requires Node >= 22.19"); process.exit(1) }' \
    && test -s /usr/share/wordlists/dirb/common.txt \
    && for tool in sshpass proxychains4 ncat chisel dirsearch feroxbuster nuclei; do \
         command -v "$tool" >/dev/null || exit 1; \
       done \
    && npm install -g --ignore-scripts "@earendil-works/pi-coding-agent@${PI_VERSION}" \
    && pi --version \
    && setcap -r /usr/lib/nmap/nmap \
    && test -z "$(getcap /usr/lib/nmap/nmap)" \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* /root/.npm

ARG TARGETARCH
ARG FSCAN_VERSION=1.8.4
ARG FSCAN_AMD64_SHA256=e82ecbe3823046a27d8c39cc0a4acb498f415549946c9ff0e241838b34ed5a21
ARG FSCAN_ARM64_SHA256=9e50082fbf0a118ea32c34b214de45b4a005f28182697e9fe94f231ea137e30f

RUN case "${TARGETARCH}" in \
        amd64) fscan_asset=fscan; fscan_sha="${FSCAN_AMD64_SHA256}" ;; \
        arm64) fscan_asset=fscan_arm64; fscan_sha="${FSCAN_ARM64_SHA256}" ;; \
        *) echo "unsupported architecture for fscan: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && curl --http1.1 -fsSL --retry 3 --retry-all-errors \
        "https://github.com/shadow1ng/fscan/releases/download/${FSCAN_VERSION}/${fscan_asset}" \
        -o /usr/local/bin/fscan \
    && printf '%s  %s\n' "${fscan_sha}" /usr/local/bin/fscan | sha256sum -c - \
    && chmod 0755 /usr/local/bin/fscan \
    && fscan -help 2>&1 | grep -Fq "fscan version: ${FSCAN_VERSION}" \
    && command -v fscan >/dev/null

ENV PI_TELEMETRY=0 \
    PI_SKIP_VERSION_CHECK=1 \
    HOME=/workspace

WORKDIR /workspace
CMD ["pi", "--version"]
