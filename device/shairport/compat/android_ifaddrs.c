/*
 * android_ifaddrs.c — getifaddrs() over netlink, for API 22
 *
 * bionic declares getifaddrs __INTRODUCED_IN(24) and FireOS 5 is API 22, so
 * the struct is usable and the functions are not. This is the smallest
 * implementation that answers everything shairport-sync actually asks of it,
 * and the "actually" is load-bearing — the obvious shortcut is wrong.
 *
 * **AF_PACKET entries are not optional.** `common.c` walks the list looking
 * for sockaddr_ll to read the MAC address, and that MAC becomes the AirPlay
 * device ID. An implementation returning only AF_INET/AF_INET6 — the
 * SIOCGIFCONF ioctl route, which is far shorter — would compile, link, run,
 * and hand out an all-zero device ID. That is the failure this codebase
 * names most often: works, reports success, wrong. So the link dump is here,
 * and netlink is the route, because SIOCGIFCONF cannot report a MAC or IPv6
 * at all.
 *
 * **ifa_netmask must be non-NULL on address entries.** `rtsp.c` tests it
 * before using an interface, so leaving it NULL silently drops every
 * interface from the timing-peer list rather than failing.
 *
 * Deliberately NOT provided: ifa_broadaddr/ifa_dstaddr and ifa_data. Nothing
 * in shairport-sync reads them (checked), and inventing them would be
 * untested code answering a question nobody asks.
 *
 * Ordering is not incidental: LINKS FIRST, then addresses.
 * `mdns_tinysvcmdns.c` takes the first non-loopback AF_INET/AF_INET6 entry
 * as the host's main address, and an address message carries only an
 * interface index — so the link table has to exist before it can be named.
 */

#ifdef __ANDROID__

#include <errno.h>
#include <linux/if_packet.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#include <net/if.h>
#include <netinet/in.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include <ifaddrs.h>

int em_getifaddrs(struct ifaddrs **ifap);
void em_freeifaddrs(struct ifaddrs *ifa);

/*
 * One allocation per entry: the public struct, storage for the two sockaddrs
 * it can point at, and the name. freeifaddrs then frees whole nodes and
 * cannot leave a dangling ifa_name or ifa_addr behind.
 */
struct em_node {
  struct ifaddrs pub;
  struct sockaddr_storage addr;
  struct sockaddr_storage netmask;
  char name[IF_NAMESIZE + 1];
};

/* Interface index -> name and flags, learned from the link dump and needed
 * to fill in address entries, which carry only an index. */
struct em_link {
  int index;
  unsigned int flags;
  char name[IF_NAMESIZE + 1];
};

#define EM_MAX_LINKS 64

struct em_ctx {
  struct ifaddrs *head;
  struct ifaddrs *tail;
  struct em_link links[EM_MAX_LINKS];
  int nlinks;
};

static void em_append(struct em_ctx *ctx, struct em_node *n) {
  n->pub.ifa_next = NULL;
  if (ctx->tail)
    ctx->tail->ifa_next = &n->pub;
  else
    ctx->head = &n->pub;
  ctx->tail = &n->pub;
}

static struct em_node *em_node_new(struct em_ctx *ctx, const char *name,
                                   unsigned int flags) {
  struct em_node *n = calloc(1, sizeof(*n));
  if (!n)
    return NULL;
  if (name) {
    strncpy(n->name, name, IF_NAMESIZE);
    n->name[IF_NAMESIZE] = '\0';
  }
  n->pub.ifa_name = n->name;
  n->pub.ifa_flags = flags;
  em_append(ctx, n);
  return n;
}

static const struct em_link *em_link_find(const struct em_ctx *ctx, int index) {
  int i;
  for (i = 0; i < ctx->nlinks; i++)
    if (ctx->links[i].index == index)
      return &ctx->links[i];
  return NULL;
}

/*
 * Prefix length to netmask, for both families — rtsp.c requires ifa_netmask
 * on the v6 entries too, and a NULL there drops the interface silently.
 */
static void em_prefix_to_mask(int family, int prefixlen,
                              struct sockaddr_storage *ss) {
  unsigned char *p;
  int total, i;
  memset(ss, 0, sizeof(*ss));
  if (family == AF_INET) {
    struct sockaddr_in *sin = (struct sockaddr_in *)ss;
    sin->sin_family = AF_INET;
    p = (unsigned char *)&sin->sin_addr;
    total = 4;
  } else {
    struct sockaddr_in6 *sin6 = (struct sockaddr_in6 *)ss;
    sin6->sin6_family = AF_INET6;
    p = (unsigned char *)&sin6->sin6_addr;
    total = 16;
  }
  if (prefixlen < 0)
    prefixlen = 0;
  if (prefixlen > total * 8)
    prefixlen = total * 8;
  for (i = 0; i < total; i++) {
    if (prefixlen >= 8) {
      p[i] = 0xff;
      prefixlen -= 8;
    } else if (prefixlen > 0) {
      p[i] = (unsigned char)(0xff << (8 - prefixlen));
      prefixlen = 0;
    }
  }
}

/* ── netlink plumbing ─────────────────────────────────────────────────── */

static int em_nl_request(int fd, int type, unsigned int seq) {
  struct {
    struct nlmsghdr nlh;
    struct rtgenmsg g;
  } req;
  struct sockaddr_nl sa;

  memset(&req, 0, sizeof(req));
  memset(&sa, 0, sizeof(sa));
  sa.nl_family = AF_NETLINK;

  req.nlh.nlmsg_len = sizeof(req);
  req.nlh.nlmsg_type = type;
  req.nlh.nlmsg_flags = NLM_F_REQUEST | NLM_F_DUMP;
  req.nlh.nlmsg_seq = seq;
  /* AF_UNSPEC so one dump covers IPv4 and IPv6 together. */
  req.g.rtgen_family = AF_UNSPEC;

  return sendto(fd, &req, sizeof(req), 0, (struct sockaddr *)&sa,
                sizeof(sa)) < 0
             ? -1
             : 0;
}

static void em_handle_link(struct em_ctx *ctx, struct nlmsghdr *nlh) {
  struct ifinfomsg *ifi = NLMSG_DATA(nlh);
  struct rtattr *rta = IFLA_RTA(ifi);
  int len = nlh->nlmsg_len - NLMSG_LENGTH(sizeof(*ifi));
  const char *name = NULL;
  const unsigned char *hwaddr = NULL;
  int hwlen = 0;
  struct em_node *n;
  struct sockaddr_ll *sll;
  struct em_link *link;

  for (; RTA_OK(rta, len); rta = RTA_NEXT(rta, len)) {
    if (rta->rta_type == IFLA_IFNAME)
      name = (const char *)RTA_DATA(rta);
    else if (rta->rta_type == IFLA_ADDRESS) {
      hwaddr = (const unsigned char *)RTA_DATA(rta);
      hwlen = (int)RTA_PAYLOAD(rta);
    }
  }
  if (!name)
    return;

  if (ctx->nlinks < EM_MAX_LINKS) {
    link = &ctx->links[ctx->nlinks++];
    link->index = ifi->ifi_index;
    link->flags = ifi->ifi_flags;
    strncpy(link->name, name, IF_NAMESIZE);
    link->name[IF_NAMESIZE] = '\0';
  }

  /* The AF_PACKET entry, which is what common.c reads the MAC from. Emitted
   * even with no hardware address (a tunnel, or lo) so the interface is in
   * the list at all; sll_halen is then 0 and the caller copies nothing. */
  n = em_node_new(ctx, name, ifi->ifi_flags);
  if (!n)
    return;
  sll = (struct sockaddr_ll *)&n->addr;
  sll->sll_family = AF_PACKET;
  sll->sll_ifindex = ifi->ifi_index;
  sll->sll_hatype = ifi->ifi_type;
  if (hwaddr && hwlen > 0) {
    if (hwlen > (int)sizeof(sll->sll_addr))
      hwlen = (int)sizeof(sll->sll_addr);
    sll->sll_halen = (unsigned char)hwlen;
    memcpy(sll->sll_addr, hwaddr, (size_t)hwlen);
  }
  n->pub.ifa_addr = (struct sockaddr *)&n->addr;
  /* No netmask on a link-layer entry: common.c does not look at one, and
   * rtsp.c reaches its netmask test only for entries it has accepted by
   * family. */
}

static void em_handle_addr(struct em_ctx *ctx, struct nlmsghdr *nlh) {
  struct ifaddrmsg *ifa = NLMSG_DATA(nlh);
  struct rtattr *rta = IFA_RTA(ifa);
  int len = nlh->nlmsg_len - NLMSG_LENGTH(sizeof(*ifa));
  const void *addr = NULL;
  const char *label = NULL;
  const struct em_link *link;
  struct em_node *n;

  if (ifa->ifa_family != AF_INET && ifa->ifa_family != AF_INET6)
    return;

  for (; RTA_OK(rta, len); rta = RTA_NEXT(rta, len)) {
    /* IFA_LOCAL is our address on a point-to-point link, where IFA_ADDRESS
     * is the PEER. Preferring LOCAL when present is what stops a PPP or
     * tunnel interface reporting somebody else's address as ours. */
    if (rta->rta_type == IFA_LOCAL)
      addr = RTA_DATA(rta);
    else if (rta->rta_type == IFA_ADDRESS && !addr)
      addr = RTA_DATA(rta);
    else if (rta->rta_type == IFA_LABEL)
      label = (const char *)RTA_DATA(rta);
  }
  if (!addr)
    return;

  link = em_link_find(ctx, ifa->ifa_index);
  if (!label)
    label = link ? link->name : "";

  n = em_node_new(ctx, label, link ? link->flags : 0);
  if (!n)
    return;

  if (ifa->ifa_family == AF_INET) {
    struct sockaddr_in *sin = (struct sockaddr_in *)&n->addr;
    sin->sin_family = AF_INET;
    memcpy(&sin->sin_addr, addr, sizeof(sin->sin_addr));
  } else {
    struct sockaddr_in6 *sin6 = (struct sockaddr_in6 *)&n->addr;
    sin6->sin6_family = AF_INET6;
    memcpy(&sin6->sin6_addr, addr, sizeof(sin6->sin6_addr));
    /* A link-local address is unusable without its scope, and the kernel
     * carries that as the interface index rather than in the address. */
    sin6->sin6_scope_id = (uint32_t)ifa->ifa_index;
  }
  n->pub.ifa_addr = (struct sockaddr *)&n->addr;

  em_prefix_to_mask(ifa->ifa_family, ifa->ifa_prefixlen, &n->netmask);
  n->pub.ifa_netmask = (struct sockaddr *)&n->netmask;
}

static int em_nl_drain(int fd, struct em_ctx *ctx, unsigned int seq,
                       int is_link) {
  /* 16KB: a netlink dump arrives in datagrams, and one that does not fit is
   * TRUNCATED rather than split — so a small buffer loses interfaces with no
   * error anywhere. This is the size iproute2 uses, for this reason. */
  char buf[16384];
  for (;;) {
    ssize_t n = recv(fd, buf, sizeof(buf), 0);
    struct nlmsghdr *nlh;
    if (n < 0) {
      if (errno == EINTR)
        continue;
      return -1;
    }
    if (n == 0)
      return -1;
    for (nlh = (struct nlmsghdr *)buf; NLMSG_OK(nlh, (unsigned int)n);
         nlh = NLMSG_NEXT(nlh, n)) {
      if (nlh->nlmsg_seq != seq)
        continue;
      if (nlh->nlmsg_type == NLMSG_DONE)
        return 0;
      if (nlh->nlmsg_type == NLMSG_ERROR)
        return -1;
      if (is_link && nlh->nlmsg_type == RTM_NEWLINK)
        em_handle_link(ctx, nlh);
      else if (!is_link && nlh->nlmsg_type == RTM_NEWADDR)
        em_handle_addr(ctx, nlh);
    }
  }
}

int em_getifaddrs(struct ifaddrs **ifap) {
  struct em_ctx ctx;
  struct sockaddr_nl sa;
  int fd;

  if (!ifap)
    return -1;
  *ifap = NULL;
  memset(&ctx, 0, sizeof(ctx));

  fd = socket(AF_NETLINK, SOCK_RAW | SOCK_CLOEXEC, NETLINK_ROUTE);
  if (fd < 0)
    return -1;

  memset(&sa, 0, sizeof(sa));
  sa.nl_family = AF_NETLINK;
  if (bind(fd, (struct sockaddr *)&sa, sizeof(sa)) < 0) {
    close(fd);
    return -1;
  }

  /* Links before addresses, and not only for the ordering mdns relies on:
   * an address message carries an interface INDEX and no name or flags, so
   * the link table has to be populated first. */
  if (em_nl_request(fd, RTM_GETLINK, 1) < 0 || em_nl_drain(fd, &ctx, 1, 1) < 0)
    goto fail;
  if (em_nl_request(fd, RTM_GETADDR, 2) < 0 || em_nl_drain(fd, &ctx, 2, 0) < 0)
    goto fail;

  close(fd);
  *ifap = ctx.head;
  return 0;

fail:
  close(fd);
  em_freeifaddrs(ctx.head);
  return -1;
}

void em_freeifaddrs(struct ifaddrs *ifa) {
  /*
   * NULL is an ordinary argument here, not a caller error.
   * mdns_tinysvcmdns.c ends its second loop with `ifa == NULL` and then
   * calls freeifaddrs(ifa) — an upstream leak of one list per startup, which
   * is theirs and harmless, but it MUST NOT crash. glibc tolerates it and so
   * does this.
   */
  while (ifa) {
    struct ifaddrs *next = ifa->ifa_next;
    /* ifa is the first member of struct em_node, so it is also the
     * allocation's address — which is what makes one free() enough. */
    free(ifa);
    ifa = next;
  }
}

#endif /* __ANDROID__ */
