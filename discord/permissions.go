package discord

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

// discordActionPermissions maps every privileged bot action to the native
// Discord permission it needs. Administrator and server ownership imply all.
var discordActionPermissions = map[string]discord.Permissions{
	"ban":              discord.PermissionBanMembers,
	"dban":             discord.PermissionBanMembers,
	"tban":             discord.PermissionBanMembers,
	"sban":             discord.PermissionBanMembers,
	"kick":             discord.PermissionKickMembers,
	"mute":             discord.PermissionModerateMembers,
	"timeout":          discord.PermissionModerateMembers,
	"warn":             discord.PermissionModerateMembers,
	"unwarn":           discord.PermissionModerateMembers,
	"kill":             discord.PermissionModerateMembers,
	"dehoist":          discord.PermissionManageNicknames,
	"approvenick":      discord.PermissionManageNicknames,
	"bulkrole":         discord.PermissionManageRoles,
	"purge":            discord.PermissionManageMessages,
	"scanreactions":    discord.PermissionManageMessages,
	"ctx-reset":        discord.PermissionManageMessages,
	"stay":             discord.PermissionManageMessages,
	"notes":            discord.PermissionManageGuild,
	"memory":           discord.PermissionManageGuild,
	"refreshstarboard": discord.PermissionManageGuild,
}

// errDiscordPermission carries a reason that is safe to show the caller.
type errDiscordPermission string

func (e errDiscordPermission) Error() string { return string(e) }

// permissionDenial returns the user-facing text for an authorization error.
func permissionDenial(err error) string {
	var denied errDiscordPermission
	if errors.As(err, &denied) {
		return string(denied)
	}
	return "Couldn't confirm your Discord permissions. Try again later."
}

type discordAuthority struct {
	permissions discord.Permissions
	top         int
	topID       snowflake.ID
	owner       bool
	member      *discord.Member
}

// memberAuthority computes guild-level permissions and the highest role
// position from current REST data.
func memberAuthority(guild *discord.RestGuild, member *discord.Member) discordAuthority {
	if member.User.ID == guild.OwnerID {
		return discordAuthority{permissions: discord.PermissionsAll, top: math.MaxInt, owner: true}
	}
	authority := discordAuthority{member: member, top: -1, topID: guild.ID}
	for _, role := range guild.Roles {
		if role.ID == guild.ID {
			authority.permissions |= role.Permissions
			continue
		}
		if slices.Contains(member.RoleIDs, role.ID) {
			authority.permissions |= role.Permissions
			if role.Position > authority.top || role.Position == authority.top && role.ID < authority.topID {
				authority.top, authority.topID = role.Position, role.ID
			}
		}
	}
	if authority.permissions.Has(discord.PermissionAdministrator) {
		authority.permissions = discord.PermissionsAll
	} else if until := member.CommunicationDisabledUntil; until != nil && until.After(time.Now()) {
		authority.permissions &= discord.PermissionViewChannel | discord.PermissionReadMessageHistory
	}
	return authority
}

func (a discordAuthority) above(other discordAuthority) bool {
	return a.owner || a.top > other.top || a.top == other.top && a.topID != 0 && a.topID < other.topID
}

func (a discordAuthority) admin() bool {
	return a.owner || a.permissions.Has(discord.PermissionAdministrator)
}

func discordBotUserID(s *bot.Client) snowflake.ID {
	if s == nil {
		return 0
	}
	if id := s.ID(); id != 0 {
		return id
	}
	return s.ApplicationID
}

func (d *DiscordBanner) guild() (*discord.RestGuild, error) {
	if d == nil || d.session == nil || d.session.Rest == nil || snowflakeID(d.guildID) == 0 {
		return nil, fmt.Errorf("discord session is unavailable")
	}
	// The gateway keeps guild, roles and members current. disgo queues REST
	// calls per route and guild, so REST lookups here made moderation slow.
	if caches := d.session.Caches; caches != nil {
		if guild, ok := caches.Guild(snowflakeID(d.guildID)); ok {
			if roles := slices.Collect(caches.Roles(guild.ID)); len(roles) > 0 {
				return &discord.RestGuild{Guild: guild, Roles: roles}, nil
			}
		}
	}
	guild, err := d.session.Rest.GetGuild(snowflakeID(d.guildID), false)
	if err != nil {
		return nil, fmt.Errorf("loading guild permissions: %w", err)
	}
	return guild, nil
}

// authority fetches the member's current authority. found is false only when
// Discord reports the user is not a member.
func (d *DiscordBanner) authority(guild *discord.RestGuild, userID string) (discordAuthority, bool, error) {
	id := snowflakeID(userID)
	if id == 0 {
		return discordAuthority{}, false, fmt.Errorf("invalid Discord user ID %q", userID)
	}
	if id == guild.OwnerID {
		return discordAuthority{permissions: discord.PermissionsAll, top: math.MaxInt, owner: true}, true, nil
	}
	caches := d.session.Caches
	if caches != nil {
		if member, ok := caches.Member(guild.ID, id); ok {
			return memberAuthority(guild, &member), true, nil
		}
	}
	member, err := d.session.Rest.GetMember(guild.ID, id)
	if rest.IsJSONErrorCode(err, rest.JSONErrorCodeUnknownMember) {
		return discordAuthority{}, false, nil
	}
	if err != nil {
		return discordAuthority{}, false, fmt.Errorf("loading member %s: %w", userID, err)
	}
	if member == nil || member.User.ID != id {
		return discordAuthority{}, false, fmt.Errorf("member %s lookup returned another user", userID)
	}
	if caches != nil {
		// Member update and remove events keep this entry current from now on.
		caches.AddMember(*member)
	}
	return memberAuthority(guild, member), true, nil
}

// IsAdministrator reports whether the user owns the guild or holds the
// native Administrator permission. Lookup failures return an error.
func (d *DiscordBanner) IsAdministrator(userID string) (bool, error) {
	guild, err := d.guild()
	if err != nil {
		return false, err
	}
	authority, _, err := d.authority(guild, userID)
	return authority.admin(), err
}

// HasPermission checks that the caller currently holds the native permission
// for action. Unknown actions and failed lookups deny.
func (d *DiscordBanner) HasPermission(callerID, action string) error {
	return d.hasPermission(callerID, action, 0)
}

// HasChannelPermission applies current channel overrides for channel commands.
func (d *DiscordBanner) HasChannelPermission(callerID, action string, channelID snowflake.ID) error {
	return d.hasPermission(callerID, action, channelID)
}

func (d *DiscordBanner) hasPermission(callerID, action string, channelID snowflake.ID) error {
	need, ok := discordActionPermissions[action]
	if !ok {
		return fmt.Errorf("unknown privileged action %q", action)
	}
	guild, err := d.guild()
	if err != nil {
		return err
	}
	caller, found, err := d.authority(guild, callerID)
	if err != nil {
		return err
	}
	if !found {
		return errDiscordPermission("You don't have permission to use that command.")
	}
	permissions := caller.permissions
	if need == discord.PermissionManageMessages && !caller.admin() {
		if channelID == 0 {
			return fmt.Errorf("channel is required for action %q", action)
		}
		channel, err := d.session.Rest.GetChannel(channelID)
		if err != nil {
			return fmt.Errorf("loading channel permissions: %w", err)
		}
		guildChannel, ok := channel.(discord.GuildChannel)
		if !ok || guildChannel.GuildID() != guild.ID {
			return fmt.Errorf("channel does not belong to the configured guild")
		}
		if _, thread := channel.(discord.GuildThread); thread {
			parentID := guildChannel.ParentID()
			if parentID == nil {
				return fmt.Errorf("thread has no parent channel")
			}
			parent, err := d.session.Rest.GetChannel(*parentID)
			if err != nil {
				return fmt.Errorf("loading parent channel permissions: %w", err)
			}
			guildChannel, ok = parent.(discord.GuildChannel)
			if !ok || guildChannel.GuildID() != guild.ID {
				return fmt.Errorf("parent channel does not belong to the configured guild")
			}
		}
		overwrites := guildChannel.PermissionOverwrites()
		if overwrite, ok := overwrites.Role(guild.ID); ok {
			permissions = permissions&^overwrite.Deny | overwrite.Allow
		}
		var allow, deny discord.Permissions
		for _, roleID := range caller.member.RoleIDs {
			if roleID == guild.ID {
				continue
			}
			if overwrite, ok := overwrites.Role(roleID); ok {
				allow |= overwrite.Allow
				deny |= overwrite.Deny
			}
		}
		permissions = permissions&^deny | allow
		if overwrite, ok := overwrites.Member(caller.member.User.ID); ok {
			permissions = permissions&^overwrite.Deny | overwrite.Allow
		}
		if until := caller.member.CommunicationDisabledUntil; until != nil && until.After(time.Now()) {
			permissions &= discord.PermissionViewChannel | discord.PermissionReadMessageHistory
		}
		if !permissions.Has(discord.PermissionViewChannel) {
			return errDiscordPermission("You don't have permission to use that command in this channel.")
		}
	}
	if !permissions.Has(need) {
		return errDiscordPermission("You don't have permission to use that command.")
	}
	return nil
}

// CanManageMember checks the caller's native permission for action, the
// caller's and the bot's role hierarchy, and protects the owner and admins.
// An empty target checks only the caller's permission. A target who is not
// in the guild has no rank to protect.
func (d *DiscordBanner) CanManageMember(callerID, targetID, action string) error {
	need, ok := discordActionPermissions[action]
	if !ok {
		return fmt.Errorf("unknown privileged action %q", action)
	}
	if targetID == "" {
		return d.HasPermission(callerID, action)
	}
	if callerID == targetID {
		return errDiscordPermission("You can't use that command on yourself.")
	}
	guild, err := d.guild()
	if err != nil {
		return err
	}
	caller, found, err := d.authority(guild, callerID)
	if err != nil {
		return err
	}
	if !found || !caller.permissions.Has(need) {
		return errDiscordPermission("You don't have permission to use that command.")
	}
	botID := discordBotUserID(d.session)
	self := caller
	if botID.String() != callerID {
		if self, found, err = d.authority(guild, botID.String()); err != nil {
			return err
		} else if !found {
			return fmt.Errorf("bot member is missing from the guild")
		}
	}
	if botID.String() == targetID {
		return errDiscordPermission("I will not take action against myself.")
	}
	target, found, err := d.authority(guild, targetID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if target.admin() {
		return errDiscordPermission("I will not take action against a server administrator.")
	}
	if !caller.above(target) {
		return errDiscordPermission("Your highest role must be above the target's highest role.")
	}
	if !self.permissions.Has(need) || !self.above(target) {
		return errDiscordPermission("My role is not high enough, or I lack the Discord permission, to do that.")
	}
	return nil
}
