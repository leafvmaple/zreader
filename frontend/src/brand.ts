// The name shown to people using the app.
//
// Deliberately separate from the project's technical identity: the repo,
// the Docker image, the API paths, the localStorage keys and the container
// name all stay `zreader`. Those are stable identifiers that other things
// depend on — changing them would ripple into image tags, the compose file
// on the server, and every stored preference — while this is just what the
// header says.
export const APP_NAME = '枫读';
