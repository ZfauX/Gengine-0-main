# ER-диаграмма базы данных Gengine

Диаграмма отражает основные сущности и связи между ними. Для компактности некоторые промежуточные таблицы (многие ко многим) показаны как связи напрямую.

## Mermaid-диаграмма

```mermaid
erDiagram
    User {
        uint ID
        string Email
        string Password
        string Name
        string Role
        bool EmailVerified
        string AvatarPath
        string ProfileVisibility
        string Plan
        string StripeCustomerID
    }

    Achievement {
        uint ID
        string Code
        string Name
        string Description
        string Icon
    }

    ExternalLogin {
        uint ID
        uint UserID
        string Provider
        string ExternalID
        string AccessToken
        string RefreshToken
        time ExpiresAt
    }

    PasswordResetToken {
        uint ID
        uint UserID
        string Token
        time ExpiresAt
    }

    EmailVerificationToken {
        uint ID
        uint UserID
        string Token
        time ExpiresAt
    }

    PushSubscription {
        uint ID
        uint UserID
        string Endpoint
        string Auth
        string P256dh
    }

    NotificationSetting {
        uint ID
        uint UserID
        string SettingsJSON
    }

    Game {
        uint ID
        string Name
        string Description
        uint AuthorID
        bool IsDraft
        string Visibility
        time StartsAt
        time RegistrationDeadline
        int MaxTeamNumber
        string CoverPath
    }

    GameSetting {
        uint ID
        uint GameID
        bool AllowHints
        int HintPenaltySeconds
        int MaxHints
        int PerLevelTimeLimit
        bool HideAnswersUntilFinished
        bool AutoStart
    }

    GamePassing {
        uint ID
        uint GameID
        uint TeamID
        string Status
        time ResultDuration
        int Place
    }

    LevelProgress {
        uint ID
        uint GamePassingID
        uint LevelID
        time StartedAt
        time FinishedAt
        int HintsUsed
        int PenaltySeconds
    }

    Attempt {
        uint ID
        uint LevelProgressID
        string Code
        string FilePath
        bool IsFile
        bool Success
    }

    CoAuthor {
        uint ID
        uint GameID
        uint UserID
        string Role
    }

    Note {
        uint ID
        uint GameID
        uint UserID
        uint LevelID
        string Text
    }

    Review {
        uint ID
        uint GameID
        uint UserID
        int Rating
        string Comment
    }

    Photo {
        uint ID
        uint GameID
        uint UserID
        uint LevelID
        string Path
    }

    PlayerRating {
        uint UserID
        int Score
        time UpdatedAt
    }

    Log {
        uint ID
        uint GamePassingID
        uint LevelID
        string Message
    }

    Level {
        uint ID
        uint GameID
        string Name
        string Description
        int Position
        string Type
        uint ParentID
        uint GroupID
        int MinChildren
        bool RequiresConfirmation
        float Latitude
        float Longitude
    }

    Question {
        uint ID
        uint LevelID
        string Text
        string Hint
    }

    Answer {
        uint ID
        uint QuestionID
        string Code
    }

    MiniGame {
        uint ID
        uint LevelID
        string Type
        string Answer
        string Config
    }

    Team {
        uint ID
        string Name
        uint CaptainID
    }

    Invitation {
        uint ID
        uint TeamID
        uint UserID
        string Status
        time ExpiresAt
    }

    Tournament {
        uint ID
        string Name
        string Description
        uint AuthorID
        int PointsForFirst
        int PointsForSecond
        int PointsForThird
        int PointsForParticipation
    }

    TournamentGame {
        uint ID
        uint TournamentID
        uint GameID
        int OrderIndex
    }

    TournamentTeam {
        uint ID
        uint TournamentID
        uint TeamID
    }

    TournamentResult {
        uint ID
        uint TournamentID
        uint TeamID
        int Score
        int GamesPlayed
    }

    Follow {
        uint ID
        uint FollowerID
        uint AuthorID
    }

    ChatRoom {
        uint ID
        uint GameID
        uint TeamID
        uint PassingID
        string Name
    }

    ChatMessage {
        uint ID
        uint RoomID
        uint UserID
        string Content
    }

    BlackboxVotingSession {
        uint ID
        uint GamePassingID
        uint LevelID
        bool IsOpen
        string WinnerOption
    }

    BlackboxVote {
        uint ID
        uint SessionID
        uint VoterID
        string Option
    }

    Backup {
        uint ID
        string Filename
        string FilePath
        int64 Size
        time CreatedAt
    }

    AuditLog {
        uint ID
        uint UserID
        string Action
        string ObjectType
        uint ObjectID
        string Details
    }

    %% ==== Связи ====

    User ||--o{ Game : "author"
    User ||--o{ CoAuthor : "co-author"
    User ||--o{ Review : "writes"
    User ||--o{ Note : "writes"
    User ||--o{ Photo : "uploads"
    User ||--o{ PushSubscription : "has"
    User ||--o{ ExternalLogin : "has"
    User ||--o{ PasswordResetToken : "has"
    User ||--o{ EmailVerificationToken : "has"
    User ||--o{ NotificationSetting : "has"
    User ||--o{ Follow : "follower"
    User ||--o{ Follow : "author"
    User }o--o{ Achievement : "unlocks"
    User }o--o{ Team : "member" via "team_members"

    Team ||--o{ Invitation : "sends"
    Team ||--o{ TournamentTeam : "participates"
    Team ||--o{ GamePassing : "has"
    Team ||--o{ TournamentResult : "has"

    Game ||--o{ Level : "contains"
    Game ||--|| GameSetting : "has settings"
    Game ||--o{ GamePassing : "has"
    Game ||--o{ CoAuthor : "has"
    Game ||--o{ Note : "has"
    Game ||--o{ Review : "has"
    Game ||--o{ Photo : "has"
    Game ||--o{ TournamentGame : "included in"

    Level ||--o{ Question : "contains"
    Level ||--|| MiniGame : "has"
    Level ||--o{ LevelProgress : "progress"
    Level ||--o{ Log : "log"
    Level ||--o{ Level : "parent-child"
    Level ||--o{ Level : "group"

    Question ||--o{ Answer : "has"

    GamePassing ||--o{ LevelProgress : "has"
    GamePassing ||--o{ Log : "has"
    GamePassing ||--o{ BlackboxVotingSession : "has"

    LevelProgress ||--o{ Attempt : "has"

    BlackboxVotingSession ||--o{ BlackboxVote : "has"

    ChatRoom ||--o{ ChatMessage : "has messages"
    ChatRoom }o--|| Game : "belongs to game"
    ChatRoom }o--|| Team : "belongs to team"
    ChatRoom }o--|| GamePassing : "belongs to passing"

    Tournament ||--o{ TournamentGame : "has"
    Tournament ||--o{ TournamentTeam : "has"
    Tournament ||--o{ TournamentResult : "has"

    %% ==== Легенда ====
    %% ||---o{ — один ко многим
    %% ||---|| — один к одному
    %% }o--o{ — многие ко многим